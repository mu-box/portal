//go:build windows || darwin
// +build windows darwin

// this fake lvs enables portal to compile for darwin/windows

package vipmgr

import (
	"github.com/mu-box/portal/config"
	"github.com/mu-box/portal/core"
)

type ip struct{}

func (self ip) Init() error {
	// allow to start up
	return nil
}
func (self ip) SetVip(vip core.Vip) error {
	config.Log.Warn("VIP functionality not fully supported on darwin|windows. Continuing anyways")
	return nil
}
func (self ip) DeleteVip(vip core.Vip) error {
	config.Log.Warn("VIP functionality not fully supported on darwin|windows. Continuing anyways")
	return nil
}
func (self ip) SetVips(vips []core.Vip) error {
	config.Log.Warn("VIP functionality not fully supported on darwin|windows. Continuing anyways")
	return nil
}
func (self ip) GetVips() ([]core.Vip, error) {
	config.Log.Warn("VIP functionality not fully supported on darwin|windows. Continuing anyways")
	return nil, nil
}
