package aliyunecs

import (
	"fmt"
	"strconv"
	"strings"

	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/mcnutils"
)

// configureSecurityGroupOpenAPI finds or creates a security group in the given VPC,
// and (only if newly created) authorizes required inbound rules.
func (d *Driver) configureSecurityGroupOpenAPI() error {
	vpcId := d.VpcId
	groupName := d.SecurityGroupName
	openPort := d.OpenPorts
	ecsClient, err := d.getEcsClient()
	if err != nil {
		return err
	}
	sg, err := d.findSecurityGroupByNameOpenAPI(ecsClient, vpcId, groupName)
	if err != nil {
		return err
	}
	newSG := false
	if sg == nil {
		newSG = true
		sg, err = d.createSecurityGroupOpenAPI(ecsClient, vpcId, groupName)
		if err != nil {
			return err
		}
	}
	d.SecurityGroupId = tea.StringValue(sg.SecurityGroupId)
	// 新建安全组之后需要把，安全组规则设置上去
	if newSG {
		perms := d.configureSecurityGroupPermissionsOpenAPI(sg, openPort)
		return d.authorizeSecurityGroupPermissionsOpenAPI(ecsClient, perms)
	}
	return nil
}

func (d *Driver) findSecurityGroupByNameOpenAPI(ecsClient *ecs20140526.Client, vpcId, groupName string) (*ecs20140526.DescribeSecurityGroupAttributeResponseBody, error) {
	req := &ecs20140526.DescribeSecurityGroupsRequest{
		RegionId:   tea.String(d.Region),
		VpcId:      tea.String(vpcId),
		PageNumber: tea.Int32(1),
		PageSize:   tea.Int32(50),
	}
	// 循环查找 vpc 下面的安全组
	for {
		resp, err := ecsClient.DescribeSecurityGroups(req)
		if err != nil {
			return nil, err
		}
		for _, g := range resp.Body.SecurityGroups.SecurityGroup {
			if tea.StringValue(g.SecurityGroupName) == groupName && tea.StringValue(g.VpcId) == vpcId {
				log.Infof("%s | Found existing security group (%s) in %s", d.MachineName, groupName, vpcId)
				return d.getSecurityGroupOpenAPI(ecsClient, tea.StringValue(g.SecurityGroupId))
			}
		}
		total := int(tea.Int32Value(resp.Body.TotalCount))
		page := int(tea.Int32Value(resp.Body.PageNumber))
		size := int(tea.Int32Value(resp.Body.PageSize))
		if total == 0 || page*size >= total {
			return nil, nil
		}
		req.PageNumber = tea.Int32(int32(page + 1))
	}
}

func (d *Driver) createSecurityGroupOpenAPI(ecsClient *ecs20140526.Client, vpcId, groupName string) (*ecs20140526.DescribeSecurityGroupAttributeResponseBody, error) {
	log.Infof("%s | Creating security group (%s) in %s", d.MachineName, groupName, vpcId)
	req := &ecs20140526.CreateSecurityGroupRequest{
		RegionId:          tea.String(d.Region),
		SecurityGroupName: tea.String(groupName),
		Description:       tea.String("Rancher Machine"),
		VpcId:             tea.String(vpcId),
		ClientToken:       tea.String(CreateRandomString()),
	}
	resp, err := ecsClient.CreateSecurityGroup(req)
	if err != nil {
		return nil, err
	}
	groupId := tea.StringValue(resp.Body.SecurityGroupId)
	log.Infof("%s | Waiting for group (%s) to become available", d.MachineName, groupId)
	if err := mcnutils.WaitFor(d.securityGroupAvailableFuncOpenAPI(ecsClient, groupId)); err != nil {
		return nil, err
	}
	return d.getSecurityGroupOpenAPI(ecsClient, groupId)
}

func (d *Driver) getSecurityGroupOpenAPI(ecsClient *ecs20140526.Client, id string) (*ecs20140526.DescribeSecurityGroupAttributeResponseBody, error) {
	req := &ecs20140526.DescribeSecurityGroupAttributeRequest{
		RegionId:        tea.String(d.Region),
		SecurityGroupId: tea.String(id),
	}
	resp, err := ecsClient.DescribeSecurityGroupAttribute(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (d *Driver) securityGroupAvailableFuncOpenAPI(ecsClient *ecs20140526.Client, id string) func() bool {
	return func() bool {
		// 判断安全组是否创建成功
		_, err := d.getSecurityGroupOpenAPI(ecsClient, id)
		if err == nil {
			return true
		}
		log.Debug(err)
		return false
	}
}

func (d *Driver) authorizeSecurityGroupPermissionsOpenAPI(ecsClient *ecs20140526.Client, perms []IpPermission) error {
	req := &ecs20140526.AuthorizeSecurityGroupRequest{
		RegionId:        tea.String(d.Region),
		SecurityGroupId: tea.String(d.SecurityGroupId),
		Permissions:     make([]*ecs20140526.AuthorizeSecurityGroupRequestPermissions, 0, len(perms)),
	}
	for _, p := range perms {
		req.Permissions = append(req.Permissions, &ecs20140526.AuthorizeSecurityGroupRequestPermissions{
			IpProtocol:   tea.String(p.IpProtocol),
			PortRange:    tea.String(fmt.Sprintf("%d/%d", p.FromPort, p.ToPort)),
			SourceCidrIp: tea.String(p.IpRange),
			NicType:      tea.String("intranet"),
			Policy:       tea.String("accept"),
		})
	}
	_, err := ecsClient.AuthorizeSecurityGroup(req)
	if err != nil {
		log.Warnf("%s | Failed to authorize group %s with permissions (nicType=intranet): %v",
			d.MachineName, d.SecurityGroupId, err)
		return err
	}

	return nil
}

func (d *Driver) configureSecurityGroupPermissionsOpenAPI(group *ecs20140526.DescribeSecurityGroupAttributeResponseBody, openPort []string) []IpPermission {
	hasSSHPort := false
	hasDockerPort := false
	for _, p := range group.Permissions.Permission {
		portRange := strings.Split(tea.StringValue(p.PortRange), "/")
		if len(portRange) != 2 {
			continue
		}
		fromPort, _ := strconv.Atoi(portRange[0])
		switch fromPort {
		case 22:
			hasSSHPort = true
		case dockerPort:
			hasDockerPort = true
		}
	}
	perms := make([]IpPermission, 0, 16)
	if !hasSSHPort {
		perms = append(perms, IpPermission{IpProtocol: "tcp", FromPort: 22, ToPort: 22, IpRange: ipRange})
	}
	if !hasDockerPort {
		perms = append(perms, IpPermission{IpProtocol: "tcp", FromPort: dockerPort, ToPort: dockerPort, IpRange: ipRange})
	}
	// openPort 优先
	if len(openPort) > 0 {
		for _, p := range openPort {
			port, protocol, err := SplitPortProto(p)
			if err != nil {
				log.Errorf("Open port %s formatting error", p)
				continue
			}
			perms = append(perms, IpPermission{IpProtocol: protocol, FromPort: port, ToPort: port, IpRange: ipRange})
		}
	} else {
		// 默认端口
		perms = append(perms,
			IpPermission{IpProtocol: "tcp", FromPort: 80, ToPort: 80, IpRange: ipRange},
			IpPermission{IpProtocol: "tcp", FromPort: 443, ToPort: 443, IpRange: ipRange},
			IpPermission{IpProtocol: "all", FromPort: -1, ToPort: -1, IpRange: ipRange},
			IpPermission{IpProtocol: "tcp", FromPort: 6443, ToPort: 6443, IpRange: ipRange},
			IpPermission{IpProtocol: "tcp", FromPort: 2379, ToPort: 2380, IpRange: ipRange},
			IpPermission{IpProtocol: "tcp", FromPort: 10250, ToPort: 10252, IpRange: ipRange},
			IpPermission{IpProtocol: "tcp", FromPort: 10256, ToPort: 10256, IpRange: ipRange},
			IpPermission{IpProtocol: "udp", FromPort: 4789, ToPort: 4789, IpRange: ipRange},
			IpPermission{IpProtocol: "udp", FromPort: 8472, ToPort: 8472, IpRange: ipRange},
		)
	}
	// VPC 场景追加容器网段互通
	if d.VpcId != "" || d.VSwitchId != "" {
		// 当传入了 RouteCIDR 之后才会添加这条规则
		// 如果传入了 10.42.0.0/16
		// 就代表允许来自 10.42.0.0/16 这个“容器/Pod 网段”的所有入站流量
		containerIPRange, err := GetContainerCIDR(d.RouteCIDR)
		if err == nil && containerIPRange != "" {
			perms = append(perms, IpPermission{
				IpProtocol: "all",
				FromPort:   -1,
				ToPort:     -1,
				IpRange:    containerIPRange,
			})
		} else if err != nil {
			log.Debugf("%s failed to get container cidr: %v", tea.StringValue(group.SecurityGroupId), err)
		}
	}
	log.Debugf("%s | Configuring new permissions: %v", d.MachineName, perms)
	return perms
}
