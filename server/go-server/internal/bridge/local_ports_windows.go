package bridge

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Keep all sockets open until the bridge exits. TCP and UDP must share the
// game port; retry allocation if only one protocol could bind that port.
func bindLocalPorts(c Config) ([]net.Listener, *net.UDPConn, Config, error) {
	for attempt := 0; attempt < 20; attempt++ {
		var listeners []net.Listener
		closeAll := func() {
			for _, l := range listeners {
				l.Close()
			}
		}
		ports := []*int{&c.LoginPort, &c.SDKPort, &c.GamePort}
		for _, port := range ports {
			requested := *port
			if c.AutoPorts {
				requested = 0
			}
			l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", requested))
			if err != nil {
				closeAll()
				return nil, nil, c, err
			}
			listeners = append(listeners, l)
			*port = l.Addr().(*net.TCPAddr).Port
		}
		u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.GamePort})
		if err == nil {
			return listeners, u, c, nil
		}
		closeAll()
		if !c.AutoPorts {
			return nil, nil, c, err
		}
	}
	return nil, nil, c, fmt.Errorf("无法分配同时支持 TCP 和 UDP 的本地端口，请稍后重试")
}

func writeLocalPorts(c Config) error {
	path := filepath.Join(c.ClientDirectory, "Data", "config.xml")
	xml, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(<LoginServer\b[^>]*\bPort\s*=\s*")[0-9]+(")`)
	if !re.Match(xml) {
		return fmt.Errorf("Data/config.xml 缺少 SDK 端口设置")
	}
	updated := re.ReplaceAllFunc(xml, func(b []byte) []byte {
		m := re.FindSubmatch(b)
		return []byte(string(m[1]) + strconv.Itoa(c.SDKPort) + string(m[2]))
	})
	if err = os.WriteFile(path, updated, 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.ClientDirectory, "server.ini"), []byte(fmt.Sprintf("[server]\r\nip=127.0.0.1\r\nport=%d\r\n", c.LoginPort)), 0600)
}
