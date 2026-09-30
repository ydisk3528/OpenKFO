// Operator-only SSH helper. No public listener and no OSS credentials on the server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/releases"
)

const base = "https://openkfo.oss-cn-hangzhou.aliyuncs.com/"
const root = "/opt/kungfu-go"

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

type request struct {
	Mode         string `json:"mode"`
	Version      string `json:"version"`
	ManifestHash string `json:"manifest_hash"`
	AllowRestart bool   `json:"allow_restart"`
}
type file struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type manifest struct {
	Version string `json:"version"`
	Target  string `json:"target"`
	Files   []file `json:"files"`
}

func fetch(url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, base) || strings.ContainsAny(strings.TrimPrefix(url, base), "?#\\") {
		return nil, fmt.Errorf("只允许固定 OSS 地址")
	}
	c := http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("拒绝重定向") }}
	r, err := c.Get(url)
	if err != nil {
		return nil, fmt.Errorf("读取 OSS 失败")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("OSS HTTP %d", r.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("OSS 文件读取失败或超出限制")
	}
	return b, nil
}

func configEntry(raw []byte, version string) (file, error) {
	var m manifest
	if json.Unmarshal(raw, &m) != nil || m.Version != version || m.Target != "client" {
		return file{}, fmt.Errorf("客户端清单无效")
	}
	var matches []file
	for _, f := range m.Files {
		if strings.EqualFold(f.Path, "Data/config.spf2") {
			matches = append(matches, f)
		}
	}
	if len(matches) != 1 {
		return file{}, fmt.Errorf("清单必须包含唯一的 Data/config.spf2")
	}
	f := matches[0]
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f.SHA256) || f.Size <= 0 || f.Size > 32<<20 || !strings.HasPrefix(f.URL, base+"releases/") || !strings.HasSuffix(f.URL, "/client/Data/config.spf2") {
		return file{}, fmt.Errorf("配置资源地址、大小或哈希无效")
	}
	return f, nil
}

func service(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "systemctl", action, "kungfu-go").Run(); err != nil {
		return fmt.Errorf("二区服务 %s 失败", action)
	}
	return nil
}
func health() error {
	c := http.Client{Timeout: time.Second}
	for i := 0; i < 20; i++ {
		if r, e := c.Get("http://127.0.0.1:19090/health"); e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("二区健康检查未通过")
}
func database() (*persistence.Store, error) {
	p, e := exec.Command("systemctl", "show", "kungfu-go", "--property=MainPID", "--value").Output()
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile("/proc/" + strings.TrimSpace(string(p)) + "/environ")
	if e != nil {
		return nil, fmt.Errorf("二区未运行，不能读取数据库环境")
	}
	for _, v := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(v, "KK_MYSQL_DSN=") {
			dsn := strings.TrimPrefix(v, "KK_MYSQL_DSN=")
			// Keep this helper realm-specific; never connect to realm 1 by mistake.
			parsed, err := mysql.ParseDSN(dsn)
			if err != nil || parsed.DBName != "kungfu_realm2" {
				return nil, fmt.Errorf("数据库不是二区，拒绝同步")
			}
			s, err := persistence.OpenExisting(dsn)
			if err != nil {
				return nil, fmt.Errorf("无法连接二区数据库")
			}
			return s, nil
		}
	}
	return nil, fmt.Errorf("缺少二区数据库环境")
}
func atomic(path string, b []byte) error {
	if e := os.WriteFile(path+".next", b, 0600); e != nil {
		return e
	}
	return os.Rename(path+".next", path)
}

func run(q request) (any, error) {
	if q.Mode != "status" && q.Mode != "prepare" && q.Mode != "sync" {
		return nil, fmt.Errorf("未知操作")
	}
	state := filepath.Join(root, "oss-sync")
	if e := os.MkdirAll(state, 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(filepath.Join(state, "sync.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, fmt.Errorf("同步被锁定；若上次异常中断，请管理员检查恢复记录")
	}
	lock.Close()
	defer os.Remove(filepath.Join(state, "sync.lock"))
	if _, e = os.Stat(filepath.Join(state, "recovery.json")); e == nil {
		return nil, fmt.Errorf("上次同步未完成，请管理员核对 recovery.json；禁止重复发布")
	}
	cpath := filepath.Join(root, "config.json")
	old, e := os.ReadFile(cpath)
	if e != nil {
		return nil, e
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(old, &cfg) != nil {
		return nil, fmt.Errorf("服务器配置无效")
	}
	var oldHash, versionURL string
	json.Unmarshal(cfg["config_hash"], &oldHash)
	json.Unmarshal(cfg["release_version_url"], &versionURL)
	if versionURL != base+"version/version.json" {
		return nil, fmt.Errorf("服务器没有使用固定 OSS 更新源")
	}
	s, e := database()
	if e != nil {
		return nil, e
	}
	defer s.DB.Close()
	a, e := s.StageAccess()
	if e != nil {
		return nil, fmt.Errorf("读取关卡绑定失败")
	}
	if a.ClientHash != "" && a.ClientHash != oldHash {
		return nil, fmt.Errorf("服务器和数据库哈希已不一致，请先修复")
	}
	result := map[string]any{"realm": "二区", "config_hash": oldHash, "database_hash": a.ClientHash, "restarted": false}
	if q.Mode == "status" {
		result["state"] = "ready"
		return result, nil
	}
	if !versionPattern.MatchString(q.Version) {
		return nil, fmt.Errorf("版本号无效")
	}
	mb, e := fetch(base+"manifest/"+q.Version+"/client.json", 1<<20)
	if e != nil {
		return nil, e
	}
	if releases.Hash(mb) != q.ManifestHash {
		return nil, fmt.Errorf("OSS 清单哈希与发布工具不一致")
	}
	f, e := configEntry(mb, q.Version)
	if e != nil {
		return nil, e
	}
	b, e := fetch(f.URL, f.Size)
	if e != nil {
		return nil, e
	}
	if int64(len(b)) != f.Size || releases.Hash(b) != f.SHA256 {
		return nil, fmt.Errorf("OSS 配置文件校验失败")
	}
	baseline := filepath.Join(state, "config.spf2")
	previous, e := os.ReadFile(baseline)
	if e != nil && oldHash == f.SHA256 {
		previous = b
	} else if e != nil {
		return nil, fmt.Errorf("缺少旧配置快照，请先初始化同步程序")
	}
	if releases.Hash(previous) != oldHash {
		return nil, fmt.Errorf("服务器旧配置快照哈希不一致")
	}
	if f.SHA256 != oldHash {
		tmp, e := os.CreateTemp(state, "candidate-*.spf2")
		if e != nil {
			return nil, e
		}
		name := tmp.Name()
		defer os.Remove(name)
		_, e = tmp.Write(b)
		tmp.Close()
		if e != nil {
			return nil, e
		}
		if e = desktop.VerifyStageCompatibility(baseline, name); e != nil {
			return nil, e
		}
		// Exercise all database migration constraints before committing the OSS pointer.
		tx, e := s.BeginStageRebind(oldHash, f.SHA256)
		if e != nil {
			return nil, e
		}
		if e = tx.Rollback(); e != nil {
			return nil, e
		}
		if !q.AllowRestart {
			return nil, fmt.Errorf("配置哈希变化，需要确认允许重启二区")
		}
	}
	result["target_hash"] = f.SHA256
	result["version"] = q.Version
	if q.Mode == "prepare" {
		result["state"] = "prepared"
		result["restart_required"] = oldHash != f.SHA256
		return result, nil
	}
	p, e := fetch(base+"version/version.json", 262144)
	if e != nil {
		return nil, e
	}
	var pointer struct {
		Version        string `json:"version"`
		ClientManifest string `json:"client_manifest"`
	}
	if json.Unmarshal(p, &pointer) != nil || pointer.Version != q.Version || pointer.ClientManifest != base+"manifest/"+q.Version+"/client.json" {
		return nil, fmt.Errorf("OSS 当前版本已改变，拒绝同步旧版本")
	}
	if oldHash == f.SHA256 {
		if e = health(); e != nil {
			return nil, e
		}
		if e = atomic(baseline, b); e != nil {
			return nil, e
		}
		result["state"] = "synced"
		return result, nil
	}
	backup := filepath.Join(state, "backup-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if e = os.Mkdir(backup, 0700); e != nil {
		return nil, e
	}
	for n, data := range map[string][]byte{"config.json": old, "config.spf2": previous} {
		if e = os.WriteFile(filepath.Join(backup, n), data, 0600); e != nil {
			return nil, e
		}
	}
	access, _ := json.Marshal(a)
	if e = os.WriteFile(filepath.Join(backup, "stage-access.json"), access, 0600); e != nil {
		return nil, e
	}
	recovery, _ := json.Marshal(map[string]string{"old_hash": oldHash, "new_hash": f.SHA256, "backup": backup, "version": q.Version})
	if e = atomic(filepath.Join(state, "recovery.json"), recovery); e != nil {
		return nil, e
	}
	if e = service("stop"); e != nil {
		return nil, e
	}
	tx, e := s.BeginStageRebind(oldHash, f.SHA256)
	if e != nil {
		return nil, fmt.Errorf("二区已停止，数据库同步失败，备份：%s：%w", backup, e)
	}
	defer tx.Rollback()
	cfg["config_hash"], _ = json.Marshal(f.SHA256)
	updated, _ := json.MarshalIndent(cfg, "", "  ")
	if e = atomic(cpath, updated); e != nil {
		return nil, e
	}
	if e = atomic(baseline, b); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		// COMMIT may succeed despite a lost acknowledgement. Re-read, never guess.
		a, check := s.StageAccess()
		if check != nil || a.ClientHash != f.SHA256 {
			return nil, fmt.Errorf("数据库提交未确认，二区保持停止，请检查 %s", backup)
		}
	}
	if e = service("start"); e != nil {
		return nil, e
	}
	if e = health(); e != nil {
		_ = service("stop")
		return nil, fmt.Errorf("新配置已同步但启动验证失败，二区已停止；备份：%s", backup)
	}
	if e = os.Remove(filepath.Join(state, "recovery.json")); e != nil {
		return nil, e
	}
	result["config_hash"] = f.SHA256
	result["database_hash"] = f.SHA256
	result["state"] = "synced"
	result["restarted"] = true
	return result, nil
}
func main() {
	var q request
	if e := json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&q); e != nil {
		fmt.Fprintln(os.Stderr, "请求无效")
		os.Exit(1)
	}
	r, e := run(q)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(r)
}
