package aliyunecs

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	slb20140515 "github.com/alibabacloud-go/slb-20140515/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc20160428 "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/mcnflag"
	"github.com/rancher/machine/libmachine/state"
)

const (
	driverName             = "aliyunecs"
	defaultRegion          = "cn-hangzhou"
	defaultInstanceType    = "ecs.n4.small"
	internetChargeType     = "PayByBandwidth"
	running                = "Running"
	stopped                = "Stopped"
	pending                = "Pending"
	starting               = "Starting"
	stopping               = "Stopping"
	deleted                = "Deleted"
	eipStatusInUse         = "InUse"
	eipStatusAvailable     = "Available"
	https                  = "https"
	defaultTimeout         = 60
	defaultWaitForInterval = 5
	instanceDefaultTimeout = 300
	ipRange                = "0.0.0.0/0"
	defaultSSHUser         = "root"
	timeout                = 300
	maxRetry               = 20
	sshTimeout             = 60
	defaultInterval        = 5
)

var (
	dockerPort = 2376
	swarmPort  = 3376
)

type Driver struct {
	*drivers.BaseDriver
	Id                      string
	AccessKey               string
	SecretKey               string
	Region                  string
	ImageID                 string
	SSHPassword             string
	SSHKeyPairName          string
	SSHPrivateKeyPath       string
	PublicKey               []byte
	InstanceId              string
	InstanceType            string
	PrivateIPAddress        string
	SecurityGroupId         string
	SecurityGroupName       string
	ReservationId           string
	VpcId                   string
	VSwitchId               string
	Zone                    string
	PrivateIPOnly           bool
	InternetMaxBandwidthOut int
	InternetChargeType      string
	RouteCIDR               string
	SLBID                   string
	SLBIPAddress            string
	Tags                    map[string]string
	DiskSize                int
	DiskFS                  string
	DiskCategory            string
	Description             string
	APIEndpoint             string
	SystemDiskCategory      string
	SystemDiskSize          int
	ResourceGroupId         string
	// PANDARIA
	InstanceChargeType     string
	Period                 int
	PeriodUnit             string
	SpotStrategy           string
	SpotPriceLimit         string
	SpotDuration           int
	OpenPorts              []string
	AllocatePublicStaticIP bool // 如果为 true 直接分配公网 ip 而非 eip

	ecsClient *ecs20140526.Client
	vpcClient *vpc20160428.Client
	slbClient *slb20140515.Client
}

func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{
			Name:   "aliyunecs-access-key-id",
			Usage:  "ECS Access Key ID",
			Value:  "",
			EnvVar: "ECS_ACCESS_KEY_ID",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-access-key-secret",
			Usage:  "ECS Access Key Secret",
			Value:  "",
			EnvVar: "ECS_ACCESS_KEY_SECRET",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-image-id",
			Usage:  "ECS machine image",
			EnvVar: "ECS_IMAGE_ID",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-region",
			Usage:  "ECS region, default cn-hangzhou",
			Value:  defaultRegion,
			EnvVar: "ECS_REGION",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-vpc-id",
			Usage:  "ECS VPC id",
			Value:  "",
			EnvVar: "ECS_VPC_ID",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-vswitch-id",
			Usage:  "ECS VSwitch id",
			Value:  "",
			EnvVar: "ECS_VSWITCH_ID",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-zone",
			Usage:  "ECS zone for instance",
			Value:  "",
			EnvVar: "ECS_ZONE",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-security-group",
			Usage:  "ECS VPC security group",
			Value:  "docker-machine",
			EnvVar: "ECS_SECURITY_GROUP",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-instance-type",
			Usage:  "ECS instance type",
			Value:  defaultInstanceType,
			EnvVar: "ECS_INSTANCE_TYPE",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-private-ip",
			Usage:  "ECS VPC instance private IP",
			Value:  "",
			EnvVar: "ECS_VPC_PRIVATE_IP",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-description",
			Usage:  "Description for instance",
			Value:  "",
			EnvVar: "ECS_DESCRIPTION",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-ssh-password",
			Usage:  "Set the password of the ssh user",
			EnvVar: "ECS_SSH_PASSWORD",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-ssh-keypair",
			Usage:  "Set the SSH key pair name",
			EnvVar: "ECS_SSH_KEYPAIR",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-ssh-keypath",
			Usage:  "File path of SSH private key",
			EnvVar: "ECS_SSH_KEYPATH",
		},
		mcnflag.BoolFlag{
			Name:   "aliyunecs-private-address-only",
			EnvVar: "ECS_PRIVATE_ADDR_ONLY",
			Usage:  "Only use a private IP address",
		},
		mcnflag.BoolFlag{
			Name:   "aliyunecs-allocate-public-static-ip",
			Usage:  "Allocate a public static IP directly for the instance (instead of allocating EIP)",
			EnvVar: "ECS_ALLOCATE_PUBLIC_STATIC_IP",
		},
		mcnflag.IntFlag{
			Name:   "aliyunecs-internet-max-bandwidth",
			Usage:  "Maximum bandwidth for Internet access (in Mbps), default 1",
			Value:  1,
			EnvVar: "ECS_INTERNET_MAX_BANDWIDTH",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-internet-charge-type",
			Usage:  "Internet charge type, the valid values are PayByBandwidth (default) or PayByTraffic",
			Value:  internetChargeType,
			EnvVar: "ECS_INTERNET_CHARGE_TYPE",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-route-cidr",
			Usage:  "Docker bridge CIDR for route entry in VPC",
			EnvVar: "ECS_ROUTE_CIDR",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-slb-id",
			Usage:  "SLB id for instance association",
			EnvVar: "ECS_SLB_ID",
		},
		mcnflag.StringSliceFlag{
			Name:   "aliyunecs-tag",
			Usage:  "Tags for instance",
			Value:  []string{},
			EnvVar: "ECS_TAGS",
		},
		mcnflag.IntFlag{
			Name:   "aliyunecs-disk-size",
			Usage:  "Data disk size for instance in GB",
			Value:  0,
			EnvVar: "ECS_DISK_SIZE",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-disk-fs",
			Usage:  "File system for data disk (ext4 or xfs)",
			Value:  "ext4",
			EnvVar: "ECS_DISK_FS",
		},
		mcnflag.IntFlag{
			Name:   "aliyunecs-system-disk-size",
			Usage:  "System disk size for instance in GB",
			Value:  40,
			EnvVar: "ECS_SYSTEM_DISK_SIZE",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-system-disk-category",
			Usage:  "System disk category for instance",
			EnvVar: "ECS_SYSTEM_DISK_CATEGORY",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-disk-category",
			Usage:  "Data disk category for instance",
			EnvVar: "ECS_DISK_CATEGORY",
		},
		mcnflag.BoolFlag{
			Name:   "aliyunecs-upgrade-kernel",
			Usage:  "Upgrade kernel for Ubuntu 14.04 instance (deprecated)",
			EnvVar: "ECS_UPGRADE_KERNEL",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-io-optimized",
			Usage:  "I/O optimized instance",
			Value:  "true",
			EnvVar: "ECS_IO_OPTIMIZED",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-api-endpoint",
			Usage:  "Custom API endpoint",
			Value:  "",
			EnvVar: "ECS_API_ENDPOINT",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-resource-group-id",
			Usage:  "Custom resource group id",
			Value:  "",
			EnvVar: "RESOURCE_GROUP_ID",
		},
		// PANDARIA
		mcnflag.StringFlag{
			Name:   "aliyunecs-instance-charge-type",
			Usage:  "The way instances are paid for",
			Value:  "",
			EnvVar: "INSTANCE_CHARGE_TYPE",
		},
		mcnflag.IntFlag{
			Name:   "aliyunecs-period",
			Usage:  "Length of purchased resources",
			Value:  0,
			EnvVar: "PERIOD",
		},
		mcnflag.IntFlag{
			Name:   "aliyunecs-spot-duration",
			Usage:  "Length of spot duration",
			Value:  1,
			EnvVar: "SPOT_DURATION",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-period-unit",
			Usage:  "Length of purchased resources",
			Value:  "",
			EnvVar: "PERIOD_UNIT",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-spot-strategy",
			Usage:  "Instance preemption strategy",
			Value:  "",
			EnvVar: "SPOT_STRATEGY",
		},
		mcnflag.StringFlag{
			Name:   "aliyunecs-spot-price-limit",
			Usage:  "Set the maximum price per hour for the instance",
			Value:  "",
			EnvVar: "SPOT_PRICELIMIT",
		},
		mcnflag.StringSliceFlag{
			Name:   "aliyunecs-open-port",
			Usage:  "Make the specified port number accessible from the Internet",
			Value:  []string{},
			EnvVar: "ECS_OPEN_PORT",
		},
	}
}

func NewDriver(hostName, storePath string) drivers.Driver {
	id := generateId()
	return &Driver{
		Id: id,
		BaseDriver: &drivers.BaseDriver{
			SSHUser:     defaultSSHUser,
			MachineName: hostName,
			StorePath:   storePath,
		}}
}

func (d *Driver) GetState() (state.State, error) {
	inst, err := d.getInstance()
	if err != nil {
		return state.Error, err
	}
	switch tea.StringValue(inst.Status) {
	case starting:
		return state.Starting, nil
	case running:
		return state.Running, nil
	case stopping:
		return state.Stopping, nil
	case stopped:
		return state.Stopped, nil
	default:
		return state.Error, nil
	}
}

func (d *Driver) GetSSHHostname() (string, error) {
	return d.GetIP()
}

func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	var region Region
	var err error
	d.APIEndpoint = flags.String("aliyunecs-api-endpoint")
	regionId := flags.String("aliyunecs-region")
	if d.APIEndpoint != "" {
		// Ignore the Region validation
		region = Region(regionId)
	} else {
		region, err = validateECSRegion(flags.String("aliyunecs-region"))
		if err != nil {
			return err
		}
	}
	d.AccessKey = flags.String("aliyunecs-access-key-id")
	d.SecretKey = flags.String("aliyunecs-access-key-secret")
	d.Region = string(region)
	d.ImageID = flags.String("aliyunecs-image-id")
	d.InstanceType = flags.String("aliyunecs-instance-type")
	d.VpcId = flags.String("aliyunecs-vpc-id")
	d.VSwitchId = flags.String("aliyunecs-vswitch-id")
	d.SecurityGroupName = flags.String("aliyunecs-security-group")
	d.Zone = flags.String("aliyunecs-zone")
	d.SwarmMaster = flags.Bool("swarm-master")
	d.SwarmHost = flags.String("swarm-host")
	d.SwarmDiscovery = flags.String("swarm-discovery")
	d.SSHUser = defaultSSHUser
	d.SSHPassword = flags.String("aliyunecs-ssh-password")
	d.SSHKeyPairName = flags.String("aliyunecs-ssh-keypair")
	d.SSHPrivateKeyPath = flags.String("aliyunecs-ssh-keypath")
	d.SSHPort = 22
	d.PrivateIPOnly = flags.Bool("aliyunecs-private-address-only")
	d.AllocatePublicStaticIP = flags.Bool("aliyunecs-allocate-public-static-ip")
	d.InternetMaxBandwidthOut = flags.Int("aliyunecs-internet-max-bandwidth")
	d.InternetChargeType = flags.String("aliyunecs-internet-charge-type")
	d.RouteCIDR = flags.String("aliyunecs-route-cidr")
	d.SLBID = flags.String("aliyunecs-slb-id")
	d.DiskSize = flags.Int("aliyunecs-disk-size")
	d.DiskFS = flags.String("aliyunecs-disk-fs")
	d.DiskCategory = flags.String("aliyunecs-disk-category")
	tags := flags.StringSlice("aliyunecs-tag")
	d.Description = flags.String("aliyunecs-description")
	d.SystemDiskCategory = flags.String("aliyunecs-system-disk-category")
	d.SystemDiskSize = flags.Int("aliyunecs-system-disk-size")
	d.ResourceGroupId = flags.String("aliyunecs-resource-group-id")
	// PANDARIA
	d.InstanceChargeType = flags.String("aliyunecs-instance-charge-type")
	d.Period = flags.Int("aliyunecs-period")
	d.PeriodUnit = flags.String("aliyunecs-period-unit")
	d.SpotStrategy = flags.String("aliyunecs-spot-strategy")
	d.SpotPriceLimit = flags.String("aliyunecs-spot-price-limit")
	d.SpotDuration = flags.Int("aliyunecs-spot-duration")
	d.OpenPorts = flags.StringSlice("aliyunecs-open-port")
	tagMap := make(map[string]string)
	if len(tags) > 0 {
		for _, tag := range tags {
			s := strings.Split(tag, "=")
			if len(s) != 2 {
				log.Infof("%s | Invalid tag for --aliyunecs-tag", tag)
				return fmt.Errorf("%s | Invalid tag for --aliyunecs-tag", tag)
			}
			k := strings.TrimSpace(s[0])
			v := strings.TrimSpace(s[1])
			tagMap[k] = v
		}
	}
	if len(tagMap) > 0 {
		d.Tags = tagMap
	}
	if d.RouteCIDR != "" {
		if _, _, err := net.ParseCIDR(d.RouteCIDR); err != nil {
			return fmt.Errorf("%s | Invalid CIDR value for --aliyunecs-route-cidr", d.MachineName)
		}
	}
	if d.InternetMaxBandwidthOut < 0 || d.InternetMaxBandwidthOut > 200 {
		return fmt.Errorf("%s | aliyunecs driver --aliyunecs-internet-max-bandwidth: The value should be in 1 ~ 200", d.MachineName)
	}
	if !d.PrivateIPOnly && d.InternetMaxBandwidthOut == 0 {
		d.InternetMaxBandwidthOut = 1
	}
	if !d.PrivateIPOnly && d.InternetMaxBandwidthOut == 0 {
		d.InternetMaxBandwidthOut = 1
	}
	if d.InternetChargeType != "PayByTraffic" && d.InternetChargeType != "PayByBandwidth" {
		return fmt.Errorf("Unsupported internet charge type: %s", d.InternetChargeType)
	}
	if d.AccessKey == "" {
		return fmt.Errorf("%s | aliyunecs driver requires the --aliyunecs-access-key-id option", d.MachineName)
	}
	if d.SecretKey == "" {
		return fmt.Errorf("%s | aliyunecs driver requires the --aliyunecs-access-key-secret option", d.MachineName)
	}
	//VpcId and VSwitchId are optional or required together
	if (d.VpcId == "" && d.VSwitchId != "") || (d.VpcId != "" && d.VSwitchId == "") {
		return fmt.Errorf("%s | aliyunecs driver requires both the --aliyunecs-vpc-id and --aliyunecs-vswitch-id for Virtual Private Cloud", d.MachineName)
	}
	if d.isSwarmMaster() {
		u, err := url.Parse(d.SwarmHost)
		if err != nil {
			return fmt.Errorf("error parsing swarm host: %s", err)
		}
		parts := strings.Split(u.Host, ":")
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		swarmPort = port
	}
	if d.APIEndpoint != "" {
		if d.SLBID != "" {
			return fmt.Errorf("Unsupport 'aliyunecs-slb-id' flag when the custom API endpoint is specified")
		}
	}
	if d.DiskFS != "xfs" && d.DiskFS != "ext4" {
		return fmt.Errorf("Unsupport file system for data disk: %s", d.DiskFS)
	}
	if d.SSHPrivateKeyPath == "" && d.SSHKeyPairName != "" {
		return fmt.Errorf("using --aliyunecs-keypair-name also requires --aliyunecs-ssh-keypath")
	}
	// Pandaria
	if d.InstanceChargeType == "PrePaid" && d.Period == 0 {
		return fmt.Errorf("using --instance-charge-type=PrePaid aslo requires --Period")
	}
	return nil
}

func (d *Driver) DriverName() string {
	return driverName
}

func (d *Driver) Create() error {
	// 检查准备 目前只检查 SLBID
	if err := d.checkPrereqs(); err != nil {
		return err
	}
	// 为了可以通过 SSH 链接实际例子，创建 keypair
	// 如果参数中传入了直接用参数里的 keypair
	if err := d.createKeyPair(); err != nil {
		return fmt.Errorf("%s | Failed to create key pair: %v", d.MachineName, err)
	}
	// 配置安全组，需要放开 rancher 相关的接口
	if err := d.configureSecurityGroupOpenAPI(); err != nil {
		return fmt.Errorf("%s | Failed to create rancher machine group: %v", d.MachineName, err)
	}
	// 随机生成密码，用来初始化 ssh clinet
	if d.SSHPassword == "" && d.SSHKeyPairName == "" {
		d.SSHPassword = RandomPassword()
		log.Infof("%s | Launching instance with generated password, please update password in console or log in with ssh key.", d.MachineName)
	}
	// 获得默认的 imageID 如果传入了就使用已有的
	imageID := d.getImageID()
	instanceId, err := d.createInstanceOpenAPI(imageID)
	if err != nil {
		return fmt.Errorf("%s | Failed to create ecs: %v", d.MachineName, err)
	}
	// 设置 instance ID
	d.InstanceId = instanceId
	log.Infof("%s | Create instance %s successfully", d.MachineName, d.InstanceId)

	cleanupNeeded := true
	// 如果有错误需要删除对应的 instance 和 eip
	defer func(id string) {
		if !cleanupNeeded {
			return
		}
		// 防止 d.InstanceId 有变动，重新用闭包拿下
		d.InstanceId = id
		if err := d.Remove(); err != nil {
			log.Infof("%s | Cleanup network for %s failed: %v", d.MachineName, d.InstanceId, err)
		}
	}(instanceId)
	// 等待 ECS 成功创建
	if err := d.waitForInstance(d.InstanceId, stopped, timeout); err != nil {
		return fmt.Errorf("%s | Failed to wait instance to 'stopped': %v", d.MachineName, err)
	}
	// 根据参赛配置网络 (public IP / EIP / route / SLB etc.)
	if err := d.configNetwork(); err != nil {
		return fmt.Errorf("%s | Failed to config network: %v", d.MachineName, err)
	}
	// 启动 ECS 并且初始化 SSH 客户端
	if err := d.startAndConfigureInstance(imageID, timeout); err != nil {
		return err
	}
	cleanupNeeded = false
	// 如果设置 Tag 为 ECS 添加
	if len(d.Tags) > 0 {
		log.Infof("%s | Adding tags %v to instance %s ...", d.MachineName, d.Tags, d.InstanceId)
		if err := d.addTags(); err != nil {
			log.Warnf("%s | Failed to add tags %v to instance %s: %v", d.MachineName, d.Tags, d.InstanceId, err)
		}
	}
	return nil
}

func (d *Driver) Start() error {
	if err := d.startInstance(); err != nil {
		log.Errorf("%s | Failed to start instance %s: %v", d.MachineName, d.InstanceId, err)
		return err
	}
	// Wait for running
	if err := d.waitForInstance(d.InstanceId, running, timeout); err != nil {
		log.Errorf("%s | Failed to wait instance %s running: %v", d.MachineName, d.InstanceId, err)
		return err
	}
	return nil
}

func (d *Driver) Stop() error {
	if err := d.stopInstance(false); err != nil {
		log.Errorf("%s | Failed to stop instance %s: %v", d.MachineName, d.InstanceId, err)
		return err
	}
	// Wait for stopped
	if err := d.waitForInstance(d.InstanceId, stopped, timeout); err != nil {
		log.Errorf("%s | Failed to wait instance %s stopped: %v", d.MachineName, d.InstanceId, err)
		return err
	}
	return nil
}

func (d *Driver) Remove() error {
	if d.InstanceId == "" {
		return fmt.Errorf("%s | Unknown instance id", d.MachineName)
	}
	log.Infof("%s | Remove instance %s ...", d.MachineName, d.InstanceId)
	// 如果实例在运行，先停机
	if s, err := d.GetState(); err == nil && s == state.Running {
		if err := d.Stop(); err != nil {
			return fmt.Errorf("%s | Failed to stop instance %s before removal: %w", d.MachineName, d.InstanceId, err)
		}
	}
	instance, err := d.getInstance()
	if err != nil {
		return fmt.Errorf("%s | Unable to describe the instance %s: %w", d.MachineName, d.InstanceId, err)
	}
	instanceId := tea.StringValue(instance.InstanceId)
	allocationId := ""
	if instance.EipAddress != nil {
		allocationId = tea.StringValue(instance.EipAddress.AllocationId)
	}
	var cleanupErrs []error
	if allocationId != "" && !d.AllocatePublicStaticIP {
		if err := d.unassociateEipAddress(allocationId, instanceId); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("unassociate eip: %w", err))
		}
		if err := d.releaseEipAddress(allocationId); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("release eip: %w", err))
		}
	}
	vpcId := ""
	if instance.VpcAttributes != nil {
		vpcId = tea.StringValue(instance.VpcAttributes.VpcId)
	}
	if vpcId != "" {
		if err := d.removeRouteEntry(vpcId, instanceId); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove route entry: %w", err))
		}
	}
	// 删除实例
	log.Infof("%s | Deleting instance: %s", d.MachineName, d.InstanceId)
	if err := d.deleteInstance(); err != nil {
		return fmt.Errorf("%s | Unable to delete instance %s: %w", d.MachineName, d.InstanceId, err)
	}
	// 清理本地状态
	d.InstanceId = ""
	d.IPAddress = ""
	d.PrivateIPAddress = ""
	d.Zone = ""
	// 如果有清理失败，返回一个汇总错误
	if len(cleanupErrs) > 0 {
		log.Warnf("%s | instance deleted but cleanup had %d issue(s): %v", d.MachineName, len(cleanupErrs), cleanupErrs)
	}
	return nil
}

func (d *Driver) Restart() error {
	if err := d.rebootInstance(false); err != nil {
		return fmt.Errorf("%s | Unable to restart instance %s: %s", d.MachineName, d.InstanceId, err)
	}
	return nil
}

func (d *Driver) Kill() error {
	log.Debugf("%s | Killing instance ...", d.MachineName)
	if err := d.stopInstance(true); err != nil {
		return fmt.Errorf("%s | Unable to kill instance %s: %s", d.MachineName, d.InstanceId, err)
	}
	return nil
}

func (d *Driver) GetIP() (string, error) {
	inst, err := d.getInstance()
	if err != nil {
		return "", err
	}
	return d.getIP(inst), nil
}

func (d *Driver) GetURL() (string, error) {
	ip, err := d.GetIP()
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", nil
	}
	return fmt.Sprintf("tcp://%s:%d", ip, dockerPort), nil
}
