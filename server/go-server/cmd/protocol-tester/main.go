// Protocol tester speaks the existing TLS/game protocol; no server-side debug bypass.
package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

type command struct {
	LobbyID       uint32 `json:"lobby_id"`
	CharacterName string `json:"character_name"`
	Config        string `json:"config"`
	Account       string `json:"account"`
	Password      string `json:"password"`
	ID            uint32 `json:"id"`
	Hex           string `json:"hex"`
}
type output struct {
	Time   string `json:"time"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	ID     uint32 `json:"id,omitempty"`
	Hex    string `json:"hex,omitempty"`
	Length int    `json:"length,omitempty"`
	UID    uint64 `json:"uid,omitempty"`
	Fatal  bool   `json:"fatal,omitempty"`
}

var outputLock sync.Mutex

func emit(o output) {
	outputLock.Lock()
	defer outputLock.Unlock()
	o.Time = time.Now().Format(time.RFC3339Nano)
	json.NewEncoder(os.Stdout).Encode(o)
}

type client struct {
	manualNetworkProbe bool
	observe            func(protocol.Message)
	conn               net.Conn
	reader             *bufio.Reader
	encoder            *json.Encoder
	mutex              sync.Mutex
	decoders           map[uint32]*protocol.Decoder
	uid                uint64
	p2p                uint32
}

func (c *client) send(f tunnel.Frame) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
	return c.encoder.Encode(f)
}
func (c *client) game(ch, id uint32, p []byte) error {
	data, err := protocol.Encode(protocol.Message{ID: id, Payload: p})
	if err != nil {
		return err
	}
	err = c.send(tunnel.Frame{Op: "data", Channel: ch, Data: data})
	if err == nil {
		emit(output{Kind: "send", ID: id, Hex: hex.EncodeToString(p), Length: len(p), Text: fmt.Sprintf("通道 %d", ch)})
	}
	return err
}
func (c *client) read() (tunnel.Frame, []protocol.Message, error) {
	c.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	data, err := tunnel.ReadFrame(c.reader, 2*1024*1024)
	if err != nil {
		return tunnel.Frame{}, nil, err
	}
	var f tunnel.Frame
	if err = json.Unmarshal(data, &f); err != nil {
		return f, nil, err
	}
	if f.Op == "relogin" || f.Op == "logged_out" {
		return f, nil, fmt.Errorf("服务器释放了会话")
	}
	if f.Error != "" {
		return f, nil, fmt.Errorf("服务器拒绝：%s", f.Error)
	}
	if f.Op != "data" || f.Channel == 1 {
		return f, nil, nil
	}
	decoder := c.decoders[f.Channel]
	if decoder == nil {
		decoder = &protocol.Decoder{}
		c.decoders[f.Channel] = decoder
	}
	messages, err := decoder.Feed(f.Data)
	for _, m := range messages {
		if m.ID == protocol.MsgNetworkDelayProbe && len(m.Payload) == 0 && !c.manualNetworkProbe {
			if e := c.game(f.Channel, protocol.MsgNetworkDelayReply, nil); e != nil {
				return f, messages, e
			}
		}
		if c.observe != nil {
			c.observe(m)
		}
		emit(output{Kind: "receive", ID: m.ID, Hex: hex.EncodeToString(m.Payload), Length: len(m.Payload), Text: describe(m)})
	}
	return f, messages, err
}
func describe(m protocol.Message) string {
	if m.ID == 4110 {
		rows, err := protocol.ParseBattleReport(m.Payload)
		if err != nil {
			return "客户端结算报告4110长度错误：必须696B"
		}
		var detail strings.Builder
		detail.WriteString("客户端结算报告（不是服务端发奖结果）")
		for slot, r := range rows {
			if r.UID != 0 {
				fmt.Fprintf(&detail, "；槽位%d UID=%d 血量=%d 结束原因码=%d 房间=%d 场次=%d", slot, r.UID, r.Health, r.FinishCode, r.RoomID, r.Serial)
			}
		}
		return detail.String()
	}
	if m.ID == protocol.MsgStageWaveControl {
		r, err := protocol.ParseStageWaveControl(m.Payload)
		if err != nil {
			return "闯关波次控制20572长度错误：必须40B"
		}
		return fmt.Sprintf("闯关波次=%d；-1触发客户端结束流程，不等于通关凭据", r.Wave)
	}
	if m.ID == 8071 && len(m.Payload) >= 4 {
		switch protocol.ReadUint32(m.Payload, 0) {
		case protocol.BattleEventPVEActorCreate:
			r, err := protocol.ParsePVEActorCreate(m.Payload)
			if err != nil {
				return "PVE创建怪物20400无效：要求67B且位置为有限数值"
			}
			return fmt.Sprintf("PVE创建怪物20400：申报UID=%d，实体=%d，模板原值=%d，位置=%v，朝向原值=%d；不作为生成授权", r.Sender, r.Actor, r.TemplateValue, r.Position, r.DirectionValue)
		case protocol.BattleEventPVEActorRemove:
			r, err := protocol.ParsePVEActorRemove(m.Payload)
			if err != nil {
				return "PVE移除怪物20401长度错误：要求47B"
			}
			return fmt.Sprintf("PVE移除怪物20401：申报UID=%d，实体=%d；不是击杀或通关凭据", r.Sender, r.Actor)
		}
	}
	if m.ID == 8071 && len(m.Payload) >= 4 && protocol.ReadUint32(m.Payload, 0) == protocol.BattleEventStageWaveEnd {
		r, err := protocol.ParseStageWaveEnd(m.Payload)
		if err != nil {
			return "闯关结束子消息20407长度错误：必须47B"
		}
		return fmt.Sprintf("闯关结束子消息20407：申报UID=%d，上下文原值=%d；不是已验证通关", r.Sender, r.ContextValue)
	}
	if m.ID == protocol.MsgRoomAnimationRequest {
		return "房间动作请求3410：仅确认Lua名称，请求结构未确认，不按3420布局解释或转发"
	}
	if m.ID == protocol.MsgBattlePoseRequest {
		return "战斗姿态请求3430：发送结构未确认，不按3440布局解释"
	}
	if m.ID == protocol.MsgStageWaveReport {
		r, err := protocol.ParseStageWaveReport(m.Payload)
		if err != nil {
			return "波次结束上报20571长度错误：必须40B"
		}
		return fmt.Sprintf("波次结束上报20571：波次=%d，上下文原值=%d，报告字段=%d（原生发送器写1）；不代表服务器已验证通关", r.Wave, r.ContextValue, r.ReportValue)
	}
	if m.ID == protocol.MsgBattlePose {
		r, err := protocol.ParseBattlePose(m.Payload)
		if err != nil {
			return "战斗姿态通知3440无效：必须9B，姿态范围0至3"
		}
		return fmt.Sprintf("战斗姿态通知3440：UID=%d，姿态原值=%d；不是席位锁定", r.UID, r.Pose)
	}
	if m.ID == protocol.MsgRoomAnimation {
		r, err := protocol.ParseRoomAnimation(m.Payload)
		if err != nil {
			return "房间动作通知3420长度错误：必须9B"
		}
		return fmt.Sprintf("房间动作通知3420：对象字段=%d，中间DWORD=%d，动作值=%d；对象映射及动作范围未确认，不作为发包授权", r.SubjectValue, r.UnknownValue, r.Animation)
	}
	if m.ID == protocol.MsgBattleLoading {
		r, err := protocol.ParseBattleStart(m.Payload)
		if err != nil {
			return "战斗加载4080长度错误：必须53B，请查看原始数据"
		}
		return fmt.Sprintf("加载战斗：房间=%d，主控槽位=%d，槽位0–7延迟(ms)=%v；0表示未测量，包含客户端处理和服务器调度", r.RoomID, r.ControllerSlot, r.NetworkDelay)
	}
	if m.ID == protocol.MsgNetworkDelayProbe {
		if len(m.Payload) != 0 {
			return "网络检测4150长度错误：应为空包"
		}
		return "开战前网络检测4150：客户端应返回4140空包"
	}
	if m.ID == protocol.MsgNetworkDelayReply {
		if len(m.Payload) != 0 {
			return "网络检测回执4140长度错误：应为空包"
		}
		return "网络检测空回执4140：服务器按当前检测计时，不含客户端申报延迟"
	}
	if m.ID == 3090 || m.ID == 3105 || m.ID == 3130 {
		return describeRoomMembers(m)
	}
	if m.ID == 4126 {
		key, err := protocol.ParseTitleRewardClaim(m.Payload)
		if err != nil {
			return "称号奖励领取4126长度错误：必须149B，商品编号位于+145；不是普通购买"
		}
		return fmt.Sprintf("称号奖励领取4126 商品编号=%d；服务器须验证未领取资格及候选列表，不能按免费购买处理", key)
	}
	if m.ID == 4125 {
		level, _, err := protocol.ParseTitleAward(m.Payload)
		if err != nil {
			return "称号通知4125截断：下游复制64B，不能按无载荷或单字节解析"
		}
		options, _ := protocol.ParseTitleRewardOptions(m.Payload)
		return fmt.Sprintf("称号通知4125 等级=%d（角色资料+123）长度=%d 候选商品及未解值=%v；+1至+7未解，64B为已确认读取下界", level, len(m.Payload), options)
	}
	switch m.ID {
	case 20530:
		rows, err := protocol.ParseExtendedTaskAuxRecords(m.Payload)
		if err != nil {
			return "任务附加记录20530截断：每条22B；不向原生客户端重放"
		}
		keys := make([]uint16, 0, 8)
		for i, r := range rows {
			if i == 8 {
				break
			}
			keys = append(keys, r.Key)
		}
		return fmt.Sprintf("任务附加记录20530 共%d条，前8条任务键=%v；当前原生接收循环存在越界风险，仅离线诊断，不向游戏重放", len(rows), keys)
	case 6031, 6032, 6051, 6052, 6061, 6062, 6081, 6082, 6091, 6092, 6301, 6302, 6311, 6312:
		return describeExtendedTaskAction(m)
	}
	if m.ID == 6010 || m.ID == 6020 || m.ID == 6030 || m.ID == 6040 || m.ID == 6041 || m.ID == 6042 || m.ID == 6050 || m.ID == 6060 || m.ID == 6080 || m.ID == 6090 {
		return describeTasks(m)
	}
	if m.ID == 21001 || m.ID == 21005 || m.ID == 21007 {
		return describeTraining(m)
	}
	if m.ID == 1038 || m.ID == 1020 {
		return describeVIP(m)
	}
	if m.ID == 8071 && len(m.Payload) >= 4 {
		kind := protocol.ReadUint32(m.Payload, 0)
		if (kind >= 9000 && kind <= 9002) || (kind >= 9500 && kind <= 9502) {
			return describeReliableEvent(m.Payload)
		}
	}
	if m.ID >= 21370 && m.ID <= 21374 {
		return describeRoomSettings(m)
	}
	if (m.ID >= 4201 && m.ID <= 4207) || (m.ID == 8071 && len(m.Payload) >= 4 && (protocol.ReadUint32(m.Payload, 0) == 8291 || protocol.ReadUint32(m.Payload, 0) == 8292)) {
		return describeTalisman(m)
	}
	if m.ID == 9100 {
		if len(m.Payload) == 0 {
			return "赠送9100空包无效：原生消费者要求非空"
		}
		return "赠送邮件已投递；收件人仍需查询邮件并领取附件"
	}
	if m.ID == 9110 {
		if len(m.Payload) != 2 {
			return "赠送失败9110长度错误：需要2字节错误码"
		}
		return fmt.Sprintf("赠送失败，原生错误码=%d（56仅限点券、19收件人不存在）", protocol.ReadUint16(m.Payload, 0))
	}
	if m.ID >= 3260 && m.ID <= 3267 {
		return describeSeatExchange(m)
	}
	if m.ID == protocol.MsgChangeRoomOwnerResult {
		if len(m.Payload) < 2 {
			return "移交房主4052过短：需要结果WORD"
		}
		if protocol.ReadUint16(m.Payload, 0) == 0 {
			return "移交房主成功4052；全房新房主以3160通知为准"
		}
		return fmt.Sprintf("移交房主失败4052：原生错误码=%d", protocol.ReadUint16(m.Payload, 0))
	}
	if m.ID == 1310 || m.ID == 1410 || m.ID == 1330 || m.ID == 1350 || m.ID == protocol.MsgRenewItemResult || m.ID == 1450 {
		return describeMail(m)
	}
	if m.ID == 9090 {
		r, err := protocol.ParseGiftRequest(m.Payload)
		if err != nil {
			return "赠送请求解析失败：" + err.Error()
		}
		return fmt.Sprintf("赠送9090 收件人=%q UID提示=%d 商品键=%d 点券报价=%d 货币=%d 未解字段165=%d/169=%d；成功投递后仍需收件人领取", r.RecipientName, r.RecipientUID, r.CatalogKey, r.TicketPrice, r.Currency, r.CatalogField77, r.Flag169)
	}
	if m.ID == 8071 && len(m.Payload) >= 4 && protocol.ReadUint32(m.Payload, 0) == 8155 {
		return describeScoreboard(m.Payload)
	}
	if m.ID == 1035 {
		if len(m.Payload) != 57 {
			return "荣誉历史目录长度错误：要求57字节"
		}
		return fmt.Sprintf("荣誉历史当前期/查询上限=%d；其余目录字段未完整恢复，收到目录不代表已有玩家战绩", protocol.ReadUint32(m.Payload, 0))
	}
	if m.ID == 20370 {
		return describeHonour(m.Payload)
	}
	if m.ID == 21411 {
		return describeWeaponLevels(m.Payload)
	}
	if m.ID == 21413 {
		return describeWeaponUpgradeResult(m.Payload)
	}
	if m.ID == 2421 {
		if len(m.Payload) < 377 {
			return "资料回包过短"
		}
		count := int(m.Payload[16])
		if len(m.Payload) != 377+68*count {
			return "资料回包长度与装备数不一致"
		}
		return fmt.Sprintf("玩家 UID=%d；角色类型=%d；已装备 %d 件；资料长度正确", protocol.ReadUint64(m.Payload, 8), m.Payload[139], count)
	}
	if m.ID == 2431 && len(m.Payload) >= 20 {
		return fmt.Sprintf("兵器库 UID=%d；记录 %d 条", protocol.ReadUint64(m.Payload, 8), protocol.ReadUint32(m.Payload, 16))
	}
	return fmt.Sprintf("收到 %d 字节", len(m.Payload))
}
func (c *client) wait(ch, id uint32) error {
	for n := 0; n < 64; n++ {
		f, m, err := c.read()
		if err != nil {
			return err
		}
		if f.Channel == ch {
			for _, p := range m {
				if p.ID == id {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("未收到预期协议 %d", id)
}
func connect(cmd command) (*client, error) {
	return connectObserved(cmd, nil)
}

// Tests can inspect the bootstrap packets consumed by the regular login chain.
func connectObserved(cmd command, observe func(protocol.Message)) (*client, error) {
	data, err := os.ReadFile(cmd.Config)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		URL         string `json:"url"`
		Certificate string `json:"server_certificate"`
		Hash        string `json:"config_hash"`
		Release     string `json:"client_release"`
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(endpoint.Hostname())
	if endpoint.Scheme != "tls" || (endpoint.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return nil, fmt.Errorf("本版本仅允许连接本地 TLS 测试服")
	}
	cert := cfg.Certificate
	if !filepath.IsAbs(cert) {
		cert = filepath.Join(filepath.Dir(cmd.Config), cert)
	}
	pem, err := os.ReadFile(cert)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("证书无效")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 8 * time.Second}, "tcp", endpoint.Host, &tls.Config{RootCAs: roots, ServerName: "kk-origin", MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	c := &client{conn: conn, reader: bufio.NewReader(conn), encoder: json.NewEncoder(conn), decoders: map[uint32]*protocol.Decoder{}, observe: observe}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	digest := sha256.Sum256([]byte("xfmRn9z7K1wTfvBYhpCwZmE8yLWN1oLv" + cmd.Password))
	cmd.Password = ""
	if err = c.send(tunnel.Frame{Op: "auth", Account: cmd.Account, Password: hex.EncodeToString(digest[:]), ConfigHash: cfg.Hash, ClientRelease: cfg.Release, Port: 18001}); err != nil {
		return nil, err
	}
	f, _, err := c.read()
	if err != nil {
		return nil, err
	}
	if f.Op != "auth" || f.UID == 0 {
		return nil, fmt.Errorf("无效认证响应")
	}
	c.uid = f.UID
	if err = c.send(tunnel.Frame{Op: "ready"}); err != nil {
		return nil, err
	}
	if err = c.send(tunnel.Frame{Op: "open", Channel: 1, Kind: "sdk"}); err != nil {
		return nil, err
	}
	sdk := protocol.LoginEncode(protocol.Message{ID: 1001})
	sdk[6] = 1
	if err = c.send(tunnel.Frame{Op: "data", Channel: 1, Data: sdk}); err != nil {
		return nil, err
	}
	f, _, err = c.read()
	if err != nil {
		return nil, err
	}
	if f.Channel != 1 || len(f.Data) < 10 || protocol.ReadUint16(f.Data, 8) != 1002 {
		return nil, fmt.Errorf("SDK 认证未完成")
	}
	hello := make([]byte, 96)
	protocol.WriteUint64(hello, 0, c.uid)
	protocol.WriteUint32(hello, 49, 594)
	if err = c.send(tunnel.Frame{Op: "open", Channel: 2, Kind: "game"}); err != nil {
		return nil, err
	}
	if err = c.game(2, 1010, hello); err != nil {
		return nil, err
	}
	if err = c.prepareCharacter(cmd.CharacterName); err != nil {
		return nil, err
	}
	if err = c.game(2, 3320, protocol.Uint32Bytes(1)); err != nil {
		return nil, err
	}
	if err = c.wait(2, 1201); err != nil {
		return nil, err
	}
	if err = c.send(tunnel.Frame{Op: "open", Channel: 3, Kind: "game"}); err != nil {
		return nil, err
	}
	lobbyID := cmd.LobbyID
	if lobbyID == 0 {
		lobbyID = 1
	}
	protocol.WriteUint32(hello, 8, lobbyID)
	if err = c.game(3, 2010, hello); err != nil {
		return nil, err
	}
	if err = c.wait(3, 2030); err != nil {
		return nil, err
	}
	// Use the same authenticated UDP tunnel as the launcher, without binding a
	// local game port. Rooms require this registration and 1156 identity bind.
	if err = c.registerP2P(); err != nil {
		return nil, err
	}
	ok = true
	return c, nil
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "--room-bots" {
		return roomBots()
	}
	if len(os.Args) > 1 {
		if len(os.Args) != 3 || (os.Args[1] != "--stage-catalog" && os.Args[1] != "--training-catalog") {
			return fmt.Errorf("usage: --stage-catalog or --training-catalog config.spf2")
		}
		if os.Args[1] == "--training-catalog" {
			missions, err := desktop.ReadTrainingMissions(os.Args[2])
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(missions)
		}
		maps, err := desktop.ReadStageMaps(os.Args[2])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(maps)
	}

	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 140000)
	if !input.Scan() {
		return fmt.Errorf("缺少连接参数")
	}
	var cmd command
	if err := json.Unmarshal(input.Bytes(), &cmd); err != nil {
		return err
	}
	c, err := connect(cmd)
	cmd.Password = ""
	if err != nil {
		return err
	}
	defer c.conn.Close()
	emit(output{Kind: "ready", Text: "已连接，资料查询就绪", UID: c.uid})
	failures := make(chan error, 3)
	go func() {
		for {
			_, _, err := c.read()
			if err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		for input.Scan() {
			var q command
			if err := json.Unmarshal(input.Bytes(), &q); err != nil {
				emit(output{Kind: "error", Text: "请求 JSON 无效"})
				continue
			}
			p, err := hex.DecodeString(strings.Join(strings.Fields(q.Hex), ""))
			if err != nil || len(p) > 32768 {
				emit(output{Kind: "error", Text: "十六进制内容无效或超过32768字节"})
				continue
			}
			if err = c.game(3, q.ID, p); err != nil {
				failures <- err
				return
			}
		}
		failures <- fmt.Errorf("测试器已关闭输入")
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-failures:
			return err
		case <-ticker.C:
			if err = c.send(tunnel.Frame{Op: "ping"}); err != nil {
				return err
			}
			if err = c.keepP2P(); err != nil {
				return err
			}
		}
	}
}
func main() {
	if err := run(); err != nil {
		msg := err.Error()
		fatal := strings.Contains(msg, "invalid_credentials") || strings.Contains(msg, "client_config_mismatch") || strings.Contains(msg, "仅允许") || strings.Contains(msg, "证书无效")
		emit(output{Kind: "disconnected", Text: msg, Fatal: fatal})
		os.Exit(1)
	}
}
