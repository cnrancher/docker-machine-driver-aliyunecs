package aliyunecs

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/ssh"
)

func createEcsClient(accessKeyId, accessKeySecret, regionId string) (_result *ecs20140526.Client, _err error) {
	credConfig, _err := credential.NewCredential(&credential.Config{
		Type:            tea.String("access_key"),
		AccessKeyId:     tea.String(accessKeyId),
		AccessKeySecret: tea.String(accessKeySecret),
		// to do STS，可再加 SecurityToken: tea.String(token)
	})
	if _err != nil {
		return _result, _err
	}
	cfg := &openapi.Config{
		Credential: credConfig,
		RegionId:   tea.String(regionId),
	}
	cfg.Endpoint = tea.String("ecs." + regionId + ".aliyuncs.com")
	_result = &ecs20140526.Client{}
	_result, _err = ecs20140526.NewClient(cfg)
	return _result, _err
}

func (d *Driver) getEcsClient() (*ecs20140526.Client, error) {
	if d.ecsClient == nil {
		ecsClient, err := createEcsClient(d.AccessKey, d.SecretKey, d.Region)
		if err != nil {
			return nil, fmt.Errorf("%s | esc client error: %v", d.MachineName, err)
		}
		d.ecsClient = ecsClient
	}
	return d.ecsClient, nil
}

func (d *Driver) getImageID() string {
	// User specified image explicitly
	if strings.TrimSpace(d.ImageID) != "" {
		log.Infof("%s | Creating instance with image %s ...", d.MachineName, d.ImageID)
		return d.ImageID
	}
	ecsClient, err := d.getEcsClient()
	if err != nil {
		return defaultUbuntuImageID
	}
	const (
		pageSize   int32 = 50
		startPage  int32 = 1
		ownerAlias       = "system"
	)
	runtime := &util.RuntimeOptions{}
	req := &ecs20140526.DescribeImagesRequest{
		RegionId:        tea.String(d.Region),
		ImageOwnerAlias: tea.String(ownerAlias),
		PageNumber:      tea.Int32(startPage),
		PageSize:        tea.Int32(pageSize),
	}
	for {
		resp, err := ecsClient.DescribeImagesWithOptions(req, runtime)
		if err != nil {
			log.Errorf("%s | Failed to describe images: %v", d.MachineName, err)
			break
		}
		for _, img := range resp.Body.Images.Image {
			id := tea.StringValue(img.ImageId)
			if strings.HasPrefix(id, defaultUbuntuImagePrefix) {
				log.Infof("%s | Creating instance with image %s ...", d.MachineName, id)
				return id
			}
		}
		total := tea.Int32Value(resp.Body.TotalCount)
		page := tea.Int32Value(resp.Body.PageNumber)
		size := tea.Int32Value(resp.Body.PageSize)
		if total == 0 || page*size >= total {
			break
		}
		req.PageNumber = tea.Int32(page + 1)
	}
	// Use default image if nothing found
	log.Infof("%s | Creating instance with image %s ...", d.MachineName, defaultUbuntuImageID)
	return defaultUbuntuImageID
}

func (d *Driver) startAndConfigureInstance(imageID string, timeout int) error {
	// Start instance
	log.Infof("%s | Starting instance %s ...", d.MachineName, d.InstanceId)
	if err := d.startInstance(); err != nil {
		return fmt.Errorf("%s | Failed to start instance %s: %v", d.MachineName, d.InstanceId, err)
	}
	// Wait for running
	if err := d.waitForInstance(d.InstanceId, running, timeout); err != nil {
		return fmt.Errorf("%s | Failed to wait instance to running state: %v", d.MachineName, err)
	}
	log.Infof("%s | Start instance %s successfully", d.MachineName, d.InstanceId)
	// Read instance attributes
	instance, err := d.getInstance()
	if err != nil {
		return err
	}
	// Populate driver fields
	d.Zone = tea.StringValue(instance.ZoneId)
	d.PrivateIPAddress = d.getPrivateIP(instance)
	d.IPAddress = d.getIP(instance)
	// Configure SSH + instance
	if err := d.setupInstanceOverSSH(imageID); err != nil {
		return err
	}
	log.Infof("%s | Created instance %s successfully with public IP address %s and private IP address %s",
		d.MachineName,
		d.InstanceId,
		d.IPAddress,
		d.PrivateIPAddress,
	)
	return nil
}

func (d *Driver) startInstance() error {
	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	req := &ecs20140526.StartInstanceRequest{
		InstanceId: tea.String(d.InstanceId),
	}
	runtime := &util.RuntimeOptions{}
	if _, err := cli.StartInstanceWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | start instance %s: %v", d.MachineName, d.InstanceId, err)
	}
	return nil
}

func (d *Driver) stopInstance(forceStop bool) error {
	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	req := &ecs20140526.StopInstanceRequest{
		InstanceId: tea.String(d.InstanceId),
		ForceStop:  tea.Bool(forceStop),
	}
	runtime := &util.RuntimeOptions{}
	if _, err := cli.StopInstanceWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | stop instance %s (force=%t): %v", d.MachineName, d.InstanceId, forceStop, err)
	}
	return nil
}

func (d *Driver) deleteInstance() error {
	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	req := &ecs20140526.DeleteInstanceRequest{
		InstanceId: tea.String(d.InstanceId),
		Force:      tea.Bool(false),
	}
	runtime := &util.RuntimeOptions{}
	if _, err := cli.DeleteInstanceWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | delete instance %s: %v", d.MachineName, d.InstanceId, err)
	}
	return nil
}

func (d *Driver) rebootInstance(forceStop bool) error {
	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	req := &ecs20140526.RebootInstanceRequest{
		InstanceId: tea.String(d.InstanceId),
		ForceStop:  tea.Bool(forceStop),
	}
	runtime := &util.RuntimeOptions{}
	if _, err := cli.RebootInstanceWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | reboot instance %s (force=%t): %v", d.MachineName, d.InstanceId, forceStop, err)
	}
	return nil
}

func (d *Driver) getInstance() (*ecs20140526.DescribeInstanceAttributeResponseBody, error) {
	cli, err := d.getEcsClient()
	if err != nil {
		return nil, err
	}
	req := &ecs20140526.DescribeInstanceAttributeRequest{
		InstanceId: tea.String(d.InstanceId),
	}
	runtime := &util.RuntimeOptions{}
	resp, err := cli.DescribeInstanceAttributeWithOptions(req, runtime)
	if err != nil {
		return nil, fmt.Errorf("%s | describe instance attribute error: %v", d.MachineName, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%s | describe instance attribute returned empty response", d.MachineName)
	}
	return resp.Body, nil
}

func (d *Driver) getPrivateIP(inst *ecs20140526.DescribeInstanceAttributeResponseBody) string {
	if inst == nil {
		return ""
	}
	if inst.InnerIpAddress != nil &&
		inst.InnerIpAddress.IpAddress != nil &&
		len(inst.InnerIpAddress.IpAddress) > 0 &&
		inst.InnerIpAddress.IpAddress[0] != nil {
		if ip := tea.StringValue(inst.InnerIpAddress.IpAddress[0]); ip != "" {
			return ip
		}
	}
	if inst.VpcAttributes != nil &&
		inst.VpcAttributes.PrivateIpAddress != nil &&
		inst.VpcAttributes.PrivateIpAddress.IpAddress != nil &&
		len(inst.VpcAttributes.PrivateIpAddress.IpAddress) > 0 &&
		inst.VpcAttributes.PrivateIpAddress.IpAddress[0] != nil {
		if ip := tea.StringValue(inst.VpcAttributes.PrivateIpAddress.IpAddress[0]); ip != "" {
			return ip
		}
	}
	return ""
}

func (d *Driver) getIP(inst *ecs20140526.DescribeInstanceAttributeResponseBody) string {
	if d.PrivateIPOnly {
		return d.getPrivateIP(inst)
	}
	if inst.PublicIpAddress != nil &&
		inst.PublicIpAddress.IpAddress != nil &&
		len(inst.PublicIpAddress.IpAddress) > 0 &&
		inst.PublicIpAddress.IpAddress[0] != nil &&
		tea.StringValue(inst.PublicIpAddress.IpAddress[0]) != "" {
		return tea.StringValue(inst.PublicIpAddress.IpAddress[0])
	}
	if inst.EipAddress != nil && inst.EipAddress.IpAddress != nil && tea.StringValue(inst.EipAddress.IpAddress) != "" {
		return tea.StringValue(inst.EipAddress.IpAddress)
	}
	return ""
}

func (d *Driver) waitForInstance(instanceID string, status string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = instanceDefaultTimeout
	}
	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	runtime := &util.RuntimeOptions{}
	interval := time.Duration(defaultWaitForInterval) * time.Second
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		log.Infof("%s | wait %s instance %s ...", status, d.MachineName, instanceID)
		req := &ecs20140526.DescribeInstanceAttributeRequest{
			InstanceId: tea.String(instanceID),
		}
		resp, err := cli.DescribeInstanceAttributeWithOptions(req, runtime)
		if err != nil {
			return err
		}
		if resp == nil || resp.Body == nil || resp.Body.Status == nil {
			return fmt.Errorf("%s | describe instance attribute succeeded but status is empty", d.MachineName)
		}
		if tea.StringValue(resp.Body.Status) == status {
			time.Sleep(interval)
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("%s | wait instance %s to be %s timeout after %ds", d.MachineName, instanceID, status, timeoutSec)
}

func (d *Driver) createInstanceOpenAPI(imageID string) (string, error) {
	cli, err := d.getEcsClient()
	if err != nil {
		return "", err
	}
	// Parse SpotPriceLimit only when needed.
	var spotPriceLimit *float32
	if d.InstanceChargeType == "PostPaid" &&
		d.SpotStrategy == "SpotWithPriceLimit" &&
		strings.TrimSpace(d.SpotPriceLimit) != "" {

		v, err := strconv.ParseFloat(strings.TrimSpace(d.SpotPriceLimit), 32)
		if err != nil {
			return "", fmt.Errorf("%s | SpotPriceLimit failed to parse float: %v", d.MachineName, err)
		}
		spotPriceLimit = tea.Float32(float32(v))
	}
	req := &ecs20140526.CreateInstanceRequest{
		RegionId:           tea.String(d.Region),
		InstanceName:       tea.String(d.GetMachineName()),
		Description:        tea.String(d.Description),
		ImageId:            tea.String(imageID),
		InstanceType:       tea.String(d.InstanceType),
		Password:           tea.String(d.SSHPassword),
		KeyPairName:        tea.String(d.SSHKeyPairName),
		VSwitchId:          tea.String(d.VSwitchId),
		ZoneId:             tea.String(d.Zone),
		ClientToken:        tea.String(CreateRandomString()),
		InstanceChargeType: tea.String(d.InstanceChargeType),
		Period:             tea.Int32(int32(d.Period)),
		PeriodUnit:         tea.String(d.PeriodUnit),
		SpotStrategy:       tea.String(d.SpotStrategy),
		SpotPriceLimit:     spotPriceLimit,
		SpotDuration:       tea.Int32(int32(d.SpotDuration)),
		SecurityGroupId:    tea.String(d.SecurityGroupId),
	}

	req.SystemDisk = &ecs20140526.CreateInstanceRequestSystemDisk{
		Category: tea.String(d.SystemDiskCategory),
		Size:     tea.Int32(int32(d.SystemDiskSize)),
	}
	if d.SystemDiskCategory != "" {
		req.SystemDisk.Category = tea.String(d.SystemDiskCategory)
	}
	if d.SystemDiskSize > 0 {
		req.SystemDisk.Size = tea.Int32(int32(d.SystemDiskSize))
	}
	if d.DiskSize > 0 {
		req.DataDisk = []*ecs20140526.CreateInstanceRequestDataDisk{
			{
				DiskName:           tea.String(d.MachineName + "_data"),
				Description:        tea.String("Data volume for Docker"),
				Size:               tea.Int32(int32(d.DiskSize)),
				Category:           tea.String(d.DiskCategory),
				Device:             tea.String("/dev/xvdb"),
				DeleteWithInstance: tea.Bool(true),
			},
		}
	}
	runtime := &util.RuntimeOptions{}
	resp, err := cli.CreateInstanceWithOptions(req, runtime)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.InstanceId == nil {
		return "", fmt.Errorf("create instance succeeded but InstanceId is empty")
	}
	return tea.StringValue(resp.Body.InstanceId), nil
}

func (d *Driver) uploadKeyPair(sshClient ssh.Client) error {
	command := fmt.Sprintf("mkdir -p ~/.ssh; echo '%s' > ~/.ssh/authorized_keys", string(d.PublicKey))
	log.Infof("%s | Upload the public key with command: %s", d.MachineName, command)
	output, err := sshClient.Output(command)
	if err != nil {
		return fmt.Errorf("%s | Upload command err, output: %v: %s", d.MachineName, err, output)
	}
	return nil
}

func (d *Driver) setupInstanceOverSSH(imageID string) error {
	ssh.SetDefaultClient(ssh.Native)
	sshClient, addr, err := d.newSSHClient()
	if err != nil {
		return fmt.Errorf("%s | new ssh clinet err: %v", d.MachineName, err)
	}
	if err := d.waitSSHReady(sshClient, addr); err != nil {
		return d.removeOnInitFailure(err)
	}
	if d.SSHKeyPairName == "" {
		log.Infof("%s | Uploading SSH keypair to %s ...", d.MachineName, addr)
		if err := d.uploadKeyPair(sshClient); err != nil {
			return err
		}
	}
	if isUbuntuImage(imageID) {
		d.fixAptConf(sshClient)
	}
	d.fixRoutingRules(sshClient)
	if d.DiskSize > 0 {
		d.autoFdisk(sshClient)
	}
	return nil
}

func (d *Driver) addTags() error {
	if d.InstanceId == "" {
		return fmt.Errorf("%s | instanceId is empty", d.MachineName)
	}
	if len(d.Tags) == 0 {
		return nil
	}

	cli, err := d.getEcsClient()
	if err != nil {
		return err
	}
	tags := make([]*ecs20140526.TagResourcesRequestTag, 0, len(d.Tags))
	for k, v := range d.Tags {
		kk := k
		vv := v
		tags = append(tags, &ecs20140526.TagResourcesRequestTag{
			Key:   tea.String(kk),
			Value: tea.String(vv),
		})
	}
	req := &ecs20140526.TagResourcesRequest{
		RegionId:     tea.String(d.Region),
		ResourceType: tea.String("instance"),
		ResourceId:   []*string{tea.String(d.InstanceId)},
		Tag:          tags,
	}
	runtime := &util.RuntimeOptions{}
	if _, err := cli.TagResourcesWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | tag resources for instance %s: %v", d.MachineName, d.InstanceId, err)
	}
	return nil
}

// 以下内容从旧的逻辑保留，实际上影响并不大，失败了也不会影响创建。
// 防止是 node-driver 以前用户需要的逻辑所以没有删除
func (d *Driver) fixAptConf(sshClient ssh.Client) {
	output, err := sshClient.Output("sed -i 's/Acquire::http::Proxy/#Acquire::http::Proxy/' /etc/apt/apt.conf")
	log.Debugf("%s | Update the apt.conf command err, output: %v: %s", d.MachineName, err, output)
}

// Fix the routing rules
func (d *Driver) fixRoutingRules(sshClient ssh.Client) {
	output, err := sshClient.Output("route del -net 172.16.0.0/12")
	log.Debugf("%s | Delete route command err, output: %v: %s", d.MachineName, err, output)
	output, err = sshClient.Output("if [ -e /etc/network/interfaces ]; then sed -i '/^up route add -net 172.16.0.0 netmask 255.240.0.0 gw/d' /etc/network/interfaces; fi")
	log.Debugf("%s | Fix route in /etc/network/interfaces command err, output: %v: %s", d.MachineName, err, output)
	output, err = sshClient.Output("if [ -e /etc/sysconfig/network-scripts/route-eth0 ]; then sed -i '/^172.16.0.0\\/12 via /d' /etc/sysconfig/network-scripts/route-eth0; fi")
	log.Debugf("%s | Fix route in /etc/sysconfig/network-scripts/route-eth0 command err, output: %v: %s", d.MachineName, err, output)
}

func (d *Driver) autoFdisk(sshClient ssh.Client) {
	s := autoFdiskScriptExt4
	if d.DiskFS == "xfs" {
		s = autoFdiskScriptXFS
	}
	script := fmt.Sprintf("cat > ~/machine_autofdisk.sh <<MACHINE_EOF\n%s\nMACHINE_EOF\n", s)
	output, err := sshClient.Output(script)
	output, err = sshClient.Output("bash ~/machine_autofdisk.sh")
	log.Debugf("%s | Auto Fdisk command err, output: %v: %s", d.MachineName, err, output)
}
