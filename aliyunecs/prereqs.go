package aliyunecs

import "fmt"

// 检查 SLB 是否可用
func (d *Driver) checkPrereqs() error {
	if d.SLBID == "" {
		return nil
	}
	addr, err := d.resolveSLBAddress(d.SLBID)
	if err != nil {
		return fmt.Errorf("%s | Invalid --aliyunecs-slb-id %q: %w", d.MachineName, d.SLBID, err)
	}
	d.SLBIPAddress = addr
	return nil
}

func (d *Driver) isSwarmMaster() bool {
	return d.SwarmMaster
}
