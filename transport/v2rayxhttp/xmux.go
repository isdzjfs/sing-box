package v2rayxhttp

import (
	"context"
	"crypto/rand"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

type xmuxConfig struct {
	maxConcurrency   *rangeConfig
	maxConnections   *rangeConfig
	cMaxReuseTimes   *rangeConfig
	hMaxRequestTimes *rangeConfig
	hMaxReusableSecs *rangeConfig
	hKeepAlivePeriod int64
}

func newXMuxConfig(options *option.V2RayXHTTPXMuxOptions) (*xmuxConfig, error) {
	config := &xmuxConfig{}
	if options == nil || *options == (option.V2RayXHTTPXMuxOptions{}) {
		config.maxConnections = &rangeConfig{from: 3, to: 3}
		config.hMaxRequestTimes = &rangeConfig{from: 600, to: 900}
		config.hMaxReusableSecs = &rangeConfig{from: 1800, to: 3000}
		return config, nil
	}
	config.maxConcurrency = newRangeConfig(options.MaxConcurrency)
	config.maxConnections = newRangeConfig(options.MaxConnections)
	config.cMaxReuseTimes = newRangeConfig(options.CMaxReuseTimes)
	config.hMaxRequestTimes = newRangeConfig(options.HMaxRequestTimes)
	config.hMaxReusableSecs = newRangeConfig(options.HMaxReusableSecs)
	config.hKeepAlivePeriod = options.HKeepAlivePeriod
	if config.maxConnections != nil && config.maxConnections.to > 0 &&
		config.maxConcurrency != nil && config.maxConcurrency.to > 0 {
		return nil, E.New("max_connections cannot be specified together with max_concurrency")
	}
	return config, nil
}

func (c *xmuxConfig) normalizedMaxConcurrency() *rangeConfig {
	if c == nil || c.maxConcurrency == nil {
		return &rangeConfig{}
	}
	return c.maxConcurrency
}

func (c *xmuxConfig) normalizedMaxConnections() *rangeConfig {
	if c == nil || c.maxConnections == nil {
		return &rangeConfig{}
	}
	return c.maxConnections
}

func (c *xmuxConfig) normalizedCMaxReuseTimes() *rangeConfig {
	if c == nil || c.cMaxReuseTimes == nil {
		return &rangeConfig{}
	}
	return c.cMaxReuseTimes
}

func (c *xmuxConfig) normalizedHMaxRequestTimes() *rangeConfig {
	if c == nil || c.hMaxRequestTimes == nil {
		return &rangeConfig{}
	}
	return c.hMaxRequestTimes
}

func (c *xmuxConfig) normalizedHMaxReusableSecs() *rangeConfig {
	if c == nil || c.hMaxReusableSecs == nil {
		return &rangeConfig{}
	}
	return c.hMaxReusableSecs
}

func (c *xmuxConfig) httpKeepAlivePeriod() time.Duration {
	if c == nil || c.hKeepAlivePeriod == 0 {
		return 0
	}
	if c.hKeepAlivePeriod < 0 {
		return -1
	}
	return time.Duration(c.hKeepAlivePeriod) * time.Second
}

type xmuxConn interface {
	IsClosed() bool
	Close() error
}

type xmuxClient struct {
	xmuxConn     xmuxConn
	running      atomic.Int32
	leftUsage    int32
	leftRequests atomic.Int32
	unreusableAt time.Time
	notUsed      atomic.Bool
}

func (c *xmuxClient) AddRunning() {
	c.running.Add(1)
}

func (c *xmuxClient) DoneRunning() {
	c.running.Add(-1)
	c.maybeClose()
}

func (c *xmuxClient) decrementRequests() int32 {
	return c.leftRequests.Add(-1)
}

func (c *xmuxClient) maybeClose() {
	if c.notUsed.Load() && c.running.Load() <= 0 {
		common.Close(c.xmuxConn)
	}
}

type xmuxManager struct {
	sync.Mutex
	xmuxConfig  *xmuxConfig
	concurrency int32
	connections int32
	newConnFunc func() xmuxConn
	xmuxClients []*xmuxClient
}

func newXMuxManager(xmuxConfig *xmuxConfig, newConnFunc func() xmuxConn) *xmuxManager {
	return &xmuxManager{
		xmuxConfig:  xmuxConfig,
		concurrency: xmuxConfig.normalizedMaxConcurrency().rand(),
		connections: xmuxConfig.normalizedMaxConnections().rand(),
		newConnFunc: newConnFunc,
	}
}

func (m *xmuxManager) GetXMuxClient(ctx context.Context) *xmuxClient {
	m.Lock()
	defer m.Unlock()
	now := time.Now()
	for i := 0; i < len(m.xmuxClients); {
		xmuxClient := m.xmuxClients[i]
		if xmuxClient.xmuxConn.IsClosed() ||
			xmuxClient.leftUsage == 0 ||
			xmuxClient.leftRequests.Load() <= 0 ||
			(!xmuxClient.unreusableAt.IsZero() && now.After(xmuxClient.unreusableAt)) {
			xmuxClient.notUsed.Store(true)
			xmuxClient.maybeClose()
			m.xmuxClients = append(m.xmuxClients[:i], m.xmuxClients[i+1:]...)
		} else {
			i++
		}
	}
	if len(m.xmuxClients) == 0 {
		return m.newXMuxClient()
	}
	if m.connections > 0 && len(m.xmuxClients) < int(m.connections) {
		return m.newXMuxClient()
	}
	var candidates []*xmuxClient
	if m.concurrency > 0 {
		for _, xmuxClient := range m.xmuxClients {
			if xmuxClient.running.Load() < m.concurrency {
				candidates = append(candidates, xmuxClient)
			}
		}
	} else {
		candidates = m.xmuxClients
	}
	if len(candidates) == 0 {
		return m.newXMuxClient()
	}
	index := randIndex(len(candidates))
	xmuxClient := candidates[index]
	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage--
	}
	return xmuxClient
}

func (m *xmuxManager) Close() error {
	m.Lock()
	defer m.Unlock()
	var err error
	for _, xmuxClient := range m.xmuxClients {
		if closeErr := xmuxClient.xmuxConn.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	m.xmuxClients = nil
	return err
}

func (m *xmuxManager) newXMuxClient() *xmuxClient {
	xmuxClient := &xmuxClient{
		xmuxConn:  m.newConnFunc(),
		leftUsage: -1,
	}
	if x := m.xmuxConfig.normalizedCMaxReuseTimes().rand(); x > 0 {
		xmuxClient.leftUsage = x - 1
	}
	xmuxClient.leftRequests.Store(math.MaxInt32)
	if x := m.xmuxConfig.normalizedHMaxRequestTimes().rand(); x > 0 {
		xmuxClient.leftRequests.Store(x)
	}
	if x := m.xmuxConfig.normalizedHMaxReusableSecs().rand(); x > 0 {
		xmuxClient.unreusableAt = time.Now().Add(time.Duration(x) * time.Second)
	}
	m.xmuxClients = append(m.xmuxClients, xmuxClient)
	return xmuxClient
}

func randIndex(n int) int {
	if n <= 1 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(value.Int64())
}
