package route

import (
	"errors"
	"syscall"
	"testing"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	"github.com/stretchr/testify/require"
)

func TestAutoDetectInterfaceFuncKeepsBindingWithAutoRedirect(t *testing.T) {
	interfaceMonitor := &autoDetectTestInterfaceMonitor{
		defaultInterface: &control.Interface{
			Name:  "wan",
			Index: 1,
		},
	}
	networkManager := &NetworkManager{
		interfaceMonitor:       interfaceMonitor,
		autoRedirectOutputMark: 1,
	}

	bindFunc := networkManager.AutoDetectInterfaceFunc()
	require.NotNil(t, bindFunc)
	err := bindFunc("udp6", "example.com:80", autoDetectTestRawConn{})

	require.ErrorIs(t, err, errAutoDetectTestControl)
	require.Equal(t, 1, interfaceMonitor.defaultInterfaceCalls)
}

var errAutoDetectTestControl = errors.New("raw control called")

type autoDetectTestRawConn struct{}

func (autoDetectTestRawConn) Control(func(fd uintptr)) error {
	return errAutoDetectTestControl
}

func (autoDetectTestRawConn) Read(func(fd uintptr) (done bool)) error {
	return errAutoDetectTestControl
}

func (autoDetectTestRawConn) Write(func(fd uintptr) (done bool)) error {
	return errAutoDetectTestControl
}

var _ syscall.RawConn = autoDetectTestRawConn{}

type autoDetectTestInterfaceMonitor struct {
	tun.DefaultInterfaceMonitor
	defaultInterface      *control.Interface
	defaultInterfaceCalls int
}

func (m *autoDetectTestInterfaceMonitor) DefaultInterface() *control.Interface {
	m.defaultInterfaceCalls++
	return m.defaultInterface
}
