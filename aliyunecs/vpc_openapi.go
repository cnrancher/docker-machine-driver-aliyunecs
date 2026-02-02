package aliyunecs

import (
	"fmt"
	mrand "math/rand"
	"strconv"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	vpc20160428 "github.com/alibabacloud-go/vpc-20160428/v6/client"
	credential "github.com/aliyun/credentials-go/credentials"
	"github.com/rancher/machine/libmachine/log"
)

// createVpcClient creates a VPC client with AccessKey credential.
func createVpcClient(accessKeyId, accessKeySecret, regionId string) (_result *vpc20160428.Client, _err error) {
	credCfg := &credential.Config{
		Type:            tea.String("access_key"),
		AccessKeyId:     tea.String(accessKeyId),
		AccessKeySecret: tea.String(accessKeySecret),
	}
	cred, _err := credential.NewCredential(credCfg)
	if _err != nil {
		return _result, _err
	}
	cfg := &openapi.Config{
		Credential: cred,
		RegionId:   tea.String(regionId),
	}
	cfg.Endpoint = tea.String("vpc." + regionId + ".aliyuncs.com")
	_result = &vpc20160428.Client{}
	_result, _err = vpc20160428.NewClient(cfg)
	return _result, _err
}

func (d *Driver) getVpcClient() (*vpc20160428.Client, error) {
	if d.vpcClient == nil {
		vpcClient, err := createVpcClient(d.AccessKey, d.SecretKey, d.Region)
		if err != nil {
			return nil, fmt.Errorf("%s | vpc client error: %v", d.MachineName, err)
		}
		d.vpcClient = vpcClient
	}
	return d.vpcClient, nil
}

func (d *Driver) configNetwork() error {
	// VPC-only: 经典网络已经删除，如果 vpcId 为空则无法创建
	vpcId := d.VpcId
	if strings.TrimSpace(vpcId) == "" {
		return fmt.Errorf("%s | vpcId is empty (classic network is not supported)", d.MachineName)
	}
	instanceId := d.InstanceId
	if err := d.addRouteEntryOpenAPI(vpcId); err != nil {
		return err
	}
	if !d.PrivateIPOnly {
		if d.AllocatePublicStaticIP {
			// ECS 静态公网 IP
			ip, err := d.allocatePublicStaticIPOpenAPI(instanceId)
			if err != nil {
				return err
			}
			log.Infof("%s | Allocated public static IP %s for instance %s successfully", d.MachineName, ip, instanceId)
		} else {
			// ECS 分配 EIP
			if err := d.allocateAndAssociateEipOpenAPI(instanceId); err != nil {
				return err
			}
		}
	}
	// 如果是 slb 把 ecs 放到道这个 slb 之下
	if d.SLBID != "" {
		if err := d.addInstanceToSlb(instanceId); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) allocatePublicStaticIPOpenAPI(instanceId string) (string, error) {
	cli, err := d.getEcsClient()
	if err != nil {
		return "", err
	}
	runtime := &util.RuntimeOptions{}
	modReq := &ecs20140526.ModifyInstanceNetworkSpecRequest{
		InstanceId:              tea.String(instanceId),
		InternetMaxBandwidthOut: tea.Int32(int32(d.InternetMaxBandwidthOut)),
		NetworkChargeType:       tea.String(d.InternetChargeType),
		AllocatePublicIp:        tea.Bool(true),
		ClientToken:             tea.String(CreateRandomString()),
	}
	if _, err := cli.ModifyInstanceNetworkSpecWithOptions(modReq, runtime); err != nil {
		return "", fmt.Errorf("%s | Failed to modify instance network spec: %v", d.MachineName, err)
	}
	// 坚持公网 ip 是否分配完成
	ip, err := d.waitForPublicIPOpenAPI(cli, instanceId, 120)
	if err != nil {
		return "", err
	}
	return ip, nil
}

func (d *Driver) waitForPublicIPOpenAPI(cli *ecs20140526.Client, instanceId string, timeoutSec int) (string, error) {
	runtime := &util.RuntimeOptions{}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		req := &ecs20140526.DescribeInstanceAttributeRequest{
			InstanceId: tea.String(instanceId),
		}
		resp, err := cli.DescribeInstanceAttributeWithOptions(req, runtime)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Body != nil {
			if resp.Body.PublicIpAddress != nil &&
				resp.Body.PublicIpAddress.IpAddress != nil &&
				len(resp.Body.PublicIpAddress.IpAddress) > 0 {
				return tea.StringValue(resp.Body.PublicIpAddress.IpAddress[0]), nil
			}
		}

		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("%s | Timed out waiting for public IP on instance %s", d.MachineName, instanceId)
}

func (d *Driver) allocateAndAssociateEipOpenAPI(instanceId string) error {
	// 用来创建 eip
	vpcCli, err := d.getVpcClient()
	if err != nil {
		return err
	}
	runtime := &util.RuntimeOptions{}
	log.Infof("%s | Allocating EIP address for instance %s ...", d.MachineName, instanceId)
	allocReq := &vpc20160428.AllocateEipAddressRequest{
		RegionId:           tea.String(d.Region),
		Bandwidth:          tea.String(strconv.Itoa(d.InternetMaxBandwidthOut)),
		InternetChargeType: tea.String(d.InternetChargeType),
		ClientToken:        tea.String(CreateRandomString()),
	}
	allocResp, err := vpcCli.AllocateEipAddressWithOptions(allocReq, runtime)
	if err != nil {
		log.Errorf("%s | Failed to allocate EIP address: %v", d.MachineName, err)
		return fmt.Errorf("%s | Failed to allocate EIP address: %v", d.MachineName, err)
	}
	if allocResp == nil || allocResp.Body == nil || allocResp.Body.AllocationId == nil {
		return fmt.Errorf("%s | allocate eip succeeded but AllocationId is empty", d.MachineName)
	}
	allocationId := tea.StringValue(allocResp.Body.AllocationId)
	// 等待 eip 创建完成
	if err = d.waitForEipOpenAPI(vpcCli, allocationId, eipStatusAvailable, 60); err != nil {
		// 如果创建失败需要释放这个 eip
		log.Infof("%s | Releasing EIP %s ...", d.MachineName, allocationId)
		relReq := &vpc20160428.ReleaseEipAddressRequest{
			RegionId:     tea.String(d.Region),
			AllocationId: tea.String(allocationId),
		}
		if _, err2 := vpcCli.ReleaseEipAddressWithOptions(relReq, runtime); err2 != nil {
			log.Warnf("%s | Failed to release EIP address: %v", d.MachineName, err2)
		}
		return fmt.Errorf("%s | Failed to wait EIP %s to be %s: %v", d.MachineName, allocationId, eipStatusAvailable, err)
	}
	// 分配 eip 给 ecs
	log.Infof("%s | Associating EIP %s for instance %s ...", d.MachineName, allocationId, instanceId)
	assocReq := &vpc20160428.AssociateEipAddressRequest{
		RegionId:     tea.String(d.Region),
		AllocationId: tea.String(allocationId),
		InstanceId:   tea.String(instanceId),
		InstanceType: tea.String("EcsInstance"),
		ClientToken:  tea.String(CreateRandomString()),
	}
	_, err = vpcCli.AssociateEipAddressWithOptions(assocReq, runtime)
	if err != nil {
		return fmt.Errorf("Failed to associate EIP %s to instance %s: %v", allocationId, instanceId, err)
	}
	// 等待 eip 状态变成 use 状态
	if err = d.waitForEipOpenAPI(vpcCli, allocationId, eipStatusInUse, 300); err != nil {
		return fmt.Errorf("%s | Failed to wait EIP %s to be %s: %v", d.MachineName, allocationId, eipStatusInUse, err)
	}
	return nil
}

func (d *Driver) unassociateEipAddress(allocationId, instanceId string) error {
	if allocationId == "" || instanceId == "" {
		return fmt.Errorf("allocationId and instanceId must not be empty")
	}
	vpcCli, err := d.getVpcClient()
	if err != nil {
		return err
	}
	req := &vpc20160428.UnassociateEipAddressRequest{
		RegionId:     tea.String(d.Region),
		AllocationId: tea.String(allocationId),
		InstanceId:   tea.String(instanceId),
		InstanceType: tea.String("EcsInstance"),
	}
	runtime := &util.RuntimeOptions{}
	_, err = vpcCli.UnassociateEipAddressWithOptions(req, runtime)
	if err = d.waitForEipOpenAPI(vpcCli, allocationId, eipStatusAvailable, 60); err != nil {
		return fmt.Errorf("%s | Failed to wait EIP %s to be %s: %v", d.MachineName, allocationId, eipStatusAvailable, err)
	}
	return err
}

func (d *Driver) releaseEipAddress(allocationId string) error {
	if allocationId == "" {
		return fmt.Errorf("allocationId must not be empty")
	}
	vpcCli, err := d.getVpcClient()
	if err != nil {
		return err
	}
	req := &vpc20160428.ReleaseEipAddressRequest{
		RegionId:     tea.String(d.Region),
		AllocationId: tea.String(allocationId),
	}
	runtime := &util.RuntimeOptions{}
	_, err = vpcCli.ReleaseEipAddressWithOptions(req, runtime)
	return err
}

func (d *Driver) waitForEipOpenAPI(vpcCli *vpc20160428.Client, allocationId, wantStatus string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	runtime := &util.RuntimeOptions{}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		req := &vpc20160428.DescribeEipAddressesRequest{
			RegionId:     tea.String(d.Region),
			AllocationId: tea.String(allocationId),
			PageNumber:   tea.Int32(1),
			PageSize:     tea.Int32(10),
		}
		resp, err := vpcCli.DescribeEipAddressesWithOptions(req, runtime)
		if err != nil {
			return err
		}
		if resp != nil && resp.Body != nil &&
			resp.Body.EipAddresses != nil &&
			resp.Body.EipAddresses.EipAddress != nil &&
			len(resp.Body.EipAddresses.EipAddress) > 0 {

			cur := tea.StringValue(resp.Body.EipAddresses.EipAddress[0].Status)
			if cur == wantStatus {
				return nil
			}
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("%s | wait eip %s error: %v", d.MachineName, wantStatus, "time out")
}

func (d *Driver) addRouteEntryOpenAPI(vpcId string) error {
	if d.RouteCIDR == "" {
		return nil
	}
	if vpcId == "" {
		return fmt.Errorf("%s | vpcId is empty", d.MachineName)
	}
	if d.InstanceId == "" {
		return fmt.Errorf("%s | instanceId is empty, cannot create route entry", d.MachineName)
	}
	vpcCli, err := d.getVpcClient()
	if err != nil {
		return err
	}
	runtime := &util.RuntimeOptions{}
	descVpcReq := &vpc20160428.DescribeVpcsRequest{
		RegionId: tea.String(d.Region),
		VpcId:    tea.String(vpcId),
	}
	descVpcResp, err := vpcCli.DescribeVpcsWithOptions(descVpcReq, runtime)
	if err != nil {
		return fmt.Errorf("%s | Failed to describe VPC %s in region %s: %v", d.MachineName, vpcId, d.Region, err)
	}
	if descVpcResp == nil || descVpcResp.Body == nil ||
		descVpcResp.Body.Vpcs == nil || descVpcResp.Body.Vpcs.Vpc == nil ||
		len(descVpcResp.Body.Vpcs.Vpc) == 0 ||
		descVpcResp.Body.Vpcs.Vpc[0].VRouterId == nil {
		return fmt.Errorf("%s | VPC %s not found or VRouterId is empty", d.MachineName, vpcId)
	}
	vrouterId := tea.StringValue(descVpcResp.Body.Vpcs.Vpc[0].VRouterId)
	descVrReq := &vpc20160428.DescribeVRoutersRequest{
		RegionId:  tea.String(d.Region),
		VRouterId: tea.String(vrouterId),
	}
	descVrResp, err := vpcCli.DescribeVRoutersWithOptions(descVrReq, runtime)
	if err != nil {
		return fmt.Errorf("%s | Failed to describe VRouters: %v", d.MachineName, err)
	}
	if descVrResp == nil || descVrResp.Body == nil ||
		descVrResp.Body.VRouters == nil || descVrResp.Body.VRouters.VRouter == nil ||
		len(descVrResp.Body.VRouters.VRouter) == 0 ||
		descVrResp.Body.VRouters.VRouter[0].RouteTableIds == nil ||
		descVrResp.Body.VRouters.VRouter[0].RouteTableIds.RouteTableId == nil ||
		len(descVrResp.Body.VRouters.VRouter[0].RouteTableIds.RouteTableId) == 0 {
		return fmt.Errorf("%s | RouteTableId not found for VRouter %s", d.MachineName, vrouterId)
	}
	routeTableId := tea.StringValue(descVrResp.Body.VRouters.VRouter[0].RouteTableIds.RouteTableId[0])
	createReq := &vpc20160428.CreateRouteEntryRequest{
		RouteTableId:         tea.String(routeTableId),
		DestinationCidrBlock: tea.String(d.RouteCIDR),
		NextHopType:          tea.String("Instance"),
		NextHopId:            tea.String(d.InstanceId),
		ClientToken:          tea.String(CreateRandomString()),
	}
	// 有可能冲突多加入重复尝试
	for attempt := 1; attempt <= maxRetry; attempt++ {
		_, err = vpcCli.CreateRouteEntryWithOptions(createReq, runtime)
		if err == nil {
			return nil
		}
		if isRetryableRouteEntryErr(err) && attempt < maxRetry {
			time.Sleep(time.Duration(5000+mrand.Int63n(2000)) * time.Millisecond)
			continue
		}
		return fmt.Errorf("%s | Failed to create route entry (routeTableId=%s, dst=%s, nextHop=%s): %v", d.MachineName, routeTableId, d.RouteCIDR, d.InstanceId, err)
	}
	return nil
}

func (d *Driver) removeRouteEntry(vpcId, instanceId string) error {
	// 没有 routecidr 就不用 remove
	if d.RouteCIDR == "" {
		return nil
	}
	if vpcId == "" || instanceId == "" {
		return fmt.Errorf("%s | vpcId/instanceId must not be empty", d.MachineName)
	}
	vpcCli, err := d.getVpcClient() // 需要你实现：返回 *vpc20160428.Client
	if err != nil {
		return err
	}
	runtime := &util.RuntimeOptions{}
	vpcsResp, err := vpcCli.DescribeVpcsWithOptions(&vpc20160428.DescribeVpcsRequest{
		RegionId: tea.String(d.Region),
		VpcId:    tea.String(vpcId),
	}, runtime)
	if err != nil {
		return fmt.Errorf("%s | Failed to describe VPC %s in region %s: %v", d.MachineName, vpcId, d.Region, err)
	}
	if vpcsResp.Body == nil || vpcsResp.Body.Vpcs == nil || vpcsResp.Body.Vpcs.Vpc == nil || len(vpcsResp.Body.Vpcs.Vpc) == 0 {
		// 找不到 VPC 就当无需删除
		return nil
	}
	vrouterId := tea.StringValue(vpcsResp.Body.Vpcs.Vpc[0].VRouterId)
	if vrouterId == "" {
		return fmt.Errorf("%s | VRouterId is empty for VPC %s", d.MachineName, vpcId)
	}
	vrResp, err := vpcCli.DescribeVRoutersWithOptions(&vpc20160428.DescribeVRoutersRequest{
		RegionId:  tea.String(d.Region),
		VRouterId: tea.String(vpcId),
	}, runtime)
	if err != nil {
		return fmt.Errorf("%s | Failed to describe vRouters for VPC %s in region %s: %v", d.MachineName, vpcId, d.Region, err)
	}
	if vrResp.Body == nil || vrResp.Body.VRouters == nil || vrResp.Body.VRouters.VRouter == nil || len(vrResp.Body.VRouters.VRouter) == 0 {
		return nil
	}
	vrouter := vrResp.Body.VRouters.VRouter[0]
	routeTableIDs := extractRouteTableIDsFromVRouter(vrouter)
	if len(routeTableIDs) == 0 {
		return nil
	}
	for _, rtID := range routeTableIDs {
		maxResult := int32(50)
		for {
			reResp, err := vpcCli.DescribeRouteEntryListWithOptions(&vpc20160428.DescribeRouteEntryListRequest{
				RegionId:     tea.String(d.Region),
				RouteTableId: tea.String(rtID),
				NextHopId:    tea.String(instanceId),
				MaxResult:    tea.Int32(maxResult),
			}, runtime)
			if err != nil {
				return fmt.Errorf("%s | Failed to describe route entries for route table %s: %v", d.MachineName, rtID, err)
			}
			if reResp.Body == nil || reResp.Body.RouteEntrys == nil || reResp.Body.RouteEntrys.RouteEntry == nil {
				break
			}
			entries := []*vpc20160428.DescribeRouteEntryListResponseBodyRouteEntrysRouteEntry{}
			if reResp.Body != nil && reResp.Body.RouteEntrys != nil && reResp.Body.RouteEntrys.RouteEntry != nil {
				entries = reResp.Body.RouteEntrys.RouteEntry
			}
			for _, e := range entries {
				dest := tea.StringValue(e.DestinationCidrBlock)
				if dest == "" {
					continue
				}
				hopId, ok := findInstanceNextHopId(e, instanceId)
				if !ok {
					continue
				}
				for attempt := 1; attempt <= maxRetry+1; attempt++ {
					log.Infof("%s | Deleting route entry in %s for instance %s (dest=%s, nextHop=%s)...", d.MachineName, rtID, instanceId, dest, hopId)
					_, delErr := vpcCli.DeleteRouteEntryWithOptions(&vpc20160428.DeleteRouteEntryRequest{
						RegionId:             tea.String(d.Region),
						RouteTableId:         tea.String(rtID),
						DestinationCidrBlock: tea.String(dest),
						NextHopId:            tea.String(hopId),
					}, runtime)
					if delErr != nil {
						log.Errorf("%s | Failed to delete route entry (attempt %d/%d): %v", d.MachineName, attempt, maxRetry+1, delErr)
						if attempt <= maxRetry {
							time.Sleep(time.Duration(5000+mrand.Int63n(2000)) * time.Millisecond)
							continue
						}
						return fmt.Errorf("%s | Failed to delete route entry after %d times", d.MachineName, maxRetry)
					}
				}
				nt := ""
				if reResp.Body != nil && reResp.Body.NextToken != nil {
					nt = tea.StringValue(reResp.Body.NextToken)
				}
				if nt == "" {
					break
				}
			}
		}
	}
	return nil
}

func isRetryableRouteEntryErr(err error) bool {
	if se, ok := err.(*tea.SDKError); ok {
		code := tea.StringValue(se.Code)
		if se.StatusCode != nil && *se.StatusCode == 500 {
			return true
		}
		if se.StatusCode != nil && *se.StatusCode == 400 && code == "IncorrectRouteEntryStatus" {
			return true
		}
	}
	return false
}

func extractRouteTableIDsFromVRouter(vr *vpc20160428.DescribeVRoutersResponseBodyVRoutersVRouter) []string {
	out := []string{}
	if vr == nil || vr.RouteTableIds == nil {
		return out
	}
	if vr.RouteTableIds.RouteTableId != nil {
		for _, s := range vr.RouteTableIds.RouteTableId {
			if tea.StringValue(s) != "" {
				out = append(out, tea.StringValue(s))
			}
		}
	}
	return out
}

func findInstanceNextHopId(e *vpc20160428.DescribeRouteEntryListResponseBodyRouteEntrysRouteEntry, instanceId string) (string, bool) {
	if e == nil || e.NextHops == nil || e.NextHops.NextHop == nil {
		return "", false
	}
	for _, hop := range e.NextHops.NextHop {
		if hop == nil {
			continue
		}
		if tea.StringValue(hop.NextHopId) == instanceId {
			return instanceId, true
		}
	}
	return "", false
}
