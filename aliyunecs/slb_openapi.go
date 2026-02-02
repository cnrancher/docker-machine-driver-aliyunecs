package aliyunecs

import (
	"encoding/json"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	slb20140515 "github.com/alibabacloud-go/slb-20140515/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"
	"github.com/rancher/machine/libmachine/log"
)

// CreateSlbClient creates an SLB client with AccessKey credential.
func createSlbClient(accessKeyId, accessKeySecret, regionId string) (_result *slb20140515.Client, _err error) {
	credCfg := &credential.Config{
		Type:            tea.String("access_key"),
		AccessKeyId:     tea.String(accessKeyId),
		AccessKeySecret: tea.String(accessKeySecret),
		// to do STS，可再加 SecurityToken: tea.String(token)
	}
	cred, _err := credential.NewCredential(credCfg)
	if _err != nil {
		return _result, _err
	}
	cfg := &openapi.Config{
		Credential: cred,
		RegionId:   tea.String(regionId),
	}
	cfg.Endpoint = tea.String("slb." + regionId + ".aliyuncs.com")
	_result = &slb20140515.Client{}
	_result, _err = slb20140515.NewClient(cfg)
	return _result, _err
}

func (d *Driver) getSlbClient() (*slb20140515.Client, error) {
	if d.slbClient == nil {
		slbClient, err := createSlbClient(d.AccessKey, d.SecretKey, d.Region)
		if err != nil {
			return nil, fmt.Errorf("%s | slb client error: %v", d.MachineName, err)
		}
		d.slbClient = slbClient
	}
	return d.slbClient, nil
}

func (d *Driver) resolveSLBAddress(slbID string) (string, error) {
	client, err := d.getSlbClient()
	if err != nil {
		return "", err
	}
	req := &slb20140515.DescribeLoadBalancerAttributeRequest{
		LoadBalancerId: tea.String(slbID),
	}
	resp, err := client.DescribeLoadBalancerAttribute(req)
	if err != nil {
		return "", fmt.Errorf("describe load balancer attribute: %w", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Address == nil || tea.StringValue(resp.Body.Address) == "" {
		return "", fmt.Errorf("empty load balancer address returned")
	}
	return tea.StringValue(resp.Body.Address), nil
}

func (d *Driver) addInstanceToSlb(instanceId string) error {
	if d.SLBID == "" {
		return nil
	}
	client, err := d.getSlbClient()
	if err != nil {
		return err
	}
	log.Infof("%s | Adding instance %s to SLB %s ...", d.MachineName, instanceId, d.SLBID)
	type backendServer struct {
		ServerId string `json:"ServerId"`
		Weight   int    `json:"Weight"`
	}
	payload, err := json.Marshal([]backendServer{
		{ServerId: instanceId, Weight: 100},
	})
	if err != nil {
		return fmt.Errorf("%s | marshal SLB backend servers: %v", d.MachineName, err)
	}
	req := &slb20140515.AddBackendServersRequest{
		LoadBalancerId: tea.String(d.SLBID),
		BackendServers: tea.String(string(payload)),
	}
	runtime := &util.RuntimeOptions{}
	if _, err := client.AddBackendServersWithOptions(req, runtime); err != nil {
		return fmt.Errorf("%s | add instance %s to SLB %s: %v", d.MachineName, instanceId, d.SLBID, err)
	}
	return nil
}
