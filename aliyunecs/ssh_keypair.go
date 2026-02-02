package aliyunecs

import (
	"fmt"
	"os"
	"time"

	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/mcnutils"
	"github.com/rancher/machine/libmachine/ssh"
)

func (d *Driver) createKeyPair() error {
	keyPath := d.GetSSHKeyPath()
	pubKeyPath := keyPath + ".pub"
	if d.SSHPrivateKeyPath == "" {
		log.Infof("%s | Creating key pair for instance ...", d.MachineName)
		return d.generateNewKeyPair(keyPath, pubKeyPath)
	}
	return d.useExistingKeyPair(keyPath, pubKeyPath)
}

func (d *Driver) generateNewKeyPair(keyPath, pubKeyPath string) error {
	log.Debugf("%s | SSH key path: %s", d.MachineName, keyPath)
	if err := ssh.GenerateSSHKey(keyPath); err != nil {
		return fmt.Errorf("failed to generate SSH key: %w", err)
	}
	publicKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read public key: %w", err)
	}
	d.PublicKey = publicKey
	return nil
}

func (d *Driver) useExistingKeyPair(keyPath, pubKeyPath string) error {
	log.Debugf("%s | Using SSHPrivateKeyPath: %s", d.MachineName, d.SSHPrivateKeyPath)
	if err := mcnutils.CopyFile(d.SSHPrivateKeyPath, keyPath); err != nil {
		return fmt.Errorf("failed to copy private key: %w", err)
	}
	if err := mcnutils.CopyFile(d.SSHPrivateKeyPath+".pub", pubKeyPath); err != nil {
		return fmt.Errorf("failed to copy public key: %w", err)
	}
	// 如果没有设置 PublicKey 则读取本地 SSHPrivateKeyPath
	if d.PublicKey == nil || len(d.PublicKey) == 0 {
		publicKey, err := os.ReadFile(pubKeyPath)
		if err != nil {
			return fmt.Errorf("failed to read public key: %w", err)
		}
		d.PublicKey = publicKey
	}
	// SSHKeyPairName 仅用于日志和标识,对应的 key 的内容应该跟 PublicKey 保存一致
	if d.SSHKeyPairName != "" {
		log.Debugf("%s | Using existing cloud key pair: %s", d.MachineName, d.SSHKeyPairName)
	}
	return nil
}

func (d *Driver) newSSHClient() (*ssh.NativeClient, string, error) {
	ip := d.IPAddress
	port, _ := d.GetSSHPort()
	addr := fmt.Sprintf("%s:%d", ip, port)
	var auth *ssh.Auth
	if d.SSHPrivateKeyPath == "" {
		auth = &ssh.Auth{Passwords: []string{d.SSHPassword}}
	} else {
		auth = &ssh.Auth{Keys: []string{d.SSHPrivateKeyPath}}
	}
	cfg, err := ssh.NewNativeConfig(d.GetSSHUsername(), auth)
	if err != nil {
		return nil, "", err
	}
	cfg.Timeout = sshTimeout * time.Second
	return &ssh.NativeClient{Config: cfg, Hostname: ip, Port: port}, addr, nil
}

func (d *Driver) waitSSHReady(c *ssh.NativeClient, addr string) error {
	log.Infof("%s | Waiting SSH service %s is ready to connect ...", d.MachineName, addr)
	return mcnutils.WaitForSpecificOrError(func() (bool, error) {
		e := c.Shell("exit")
		return e == nil, nil
	}, maxRetry, defaultInterval*time.Second)
}

func (d *Driver) removeOnInitFailure(initErr error) error {
	removeErr := mcnutils.WaitForSpecificOrError(func() (bool, error) {
		e := d.Remove()
		return e == nil, e
	}, maxRetry, defaultInterval*time.Second)
	if removeErr != nil {
		return fmt.Errorf("%s | Unable to init and failed to delete instance %s(%s): %v", d.MachineName, d.InstanceId, d.IPAddress, removeErr)
	}
	return fmt.Errorf("%s | Unable to init instance %s(%s): %v", d.MachineName, d.InstanceId, d.IPAddress, initErr)
}
