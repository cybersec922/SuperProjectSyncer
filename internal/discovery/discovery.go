package discovery

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

const serviceType = "_sps._tcp"

type Manager struct {
	syncName string
	port     int
	onFound  func(addr string)

	mu     sync.Mutex
	server *zeroconf.Server
	ctx    context.Context
	cancel context.CancelFunc
}

func New(syncName string, port int, onFound func(addr string)) *Manager {
	return &Manager{
		syncName: syncName,
		port:     port,
		onFound:  onFound,
	}
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	txt := []string{fmt.Sprintf("name=%s", m.syncName)}
	server, err := zeroconf.Register(
		m.syncName, serviceType, "local.", m.port, txt, nil,
	)
	if err != nil {
		return fmt.Errorf("mdns register: %w", err)
	}
	m.server = server

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		server.Shutdown()
		return fmt.Errorf("mdns resolver: %w", err)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())
	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		for entry := range entries {
			m.handleEntry(entry)
		}
	}()

	go func() {
		if err := resolver.Browse(m.ctx, serviceType, "local.", entries); err != nil {
			log.Printf("[%s] mdns browse: %v", m.syncName, err)
		}
	}()

	return nil
}

func (m *Manager) handleEntry(entry *zeroconf.ServiceEntry) {
	instance := strings.TrimSuffix(entry.ServiceInstanceName(), ".")
	instance = strings.TrimSuffix(instance, "."+serviceType+".local")
	instance = strings.TrimSuffix(instance, ".local")

	matched := instance == m.syncName
	if !matched {
		for _, t := range entry.Text {
			if t == "name="+m.syncName {
				matched = true
				break
			}
		}
	}
	if !matched {
		return
	}
	if len(entry.AddrIPv4) == 0 && len(entry.AddrIPv6) == 0 {
		return
	}
	var ip net.IP
	if len(entry.AddrIPv4) > 0 {
		ip = entry.AddrIPv4[0]
	} else {
		ip = entry.AddrIPv6[0]
	}
	port := entry.Port
	if port == 0 {
		port = m.port
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	if m.onFound != nil {
		m.onFound(addr)
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.server != nil {
		m.server.Shutdown()
	}
}

func ParseListenPort(listen string) (int, error) {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(portStr)
}
