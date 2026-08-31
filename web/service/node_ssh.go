package service

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"x-ui/database/model"
	"x-ui/util/crypto"

	"golang.org/x/crypto/ssh"
)

// SSHDialer SSH连接器（用于节点终端/命令执行）
type SSHDialer struct{}

// BuildSSHClient 根据节点信息建立SSH客户端连接
func (d *SSHDialer) BuildSSHClient(node *model.Node, timeout time.Duration) (*ssh.Client, error) {
	host := node.Host
	if host == "" {
		host = node.IPv4
	}
	port := node.Port
	if port == 0 {
		port = 22
	}
	user := node.SSHUser
	if user == "" {
		user = "root"
	}
	pass, err := crypto.DecryptSecret(node.SSHPassword)
	if err != nil {
		return nil, fmt.Errorf("解密SSH密码失败: %v", err)
	}
	if pass == "" {
		return nil, fmt.Errorf("节点未配置SSH密码，请先在编辑中填写")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 测试环境跳过主机密钥校验
		Timeout:         timeout,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("TCP连接失败 %s: %v", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH握手失败: %v", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	return client, nil
}

// ExecCommand 通过SSH执行一条命令，返回stdout和stderr
func (d *SSHDialer) ExecCommand(node *model.Node, command string, timeout time.Duration) (string, string, error) {
	if command == "" {
		return "", "", fmt.Errorf("命令不能为空")
	}

	client, err := d.BuildSSHClient(node, timeout)
	if err != nil {
		return "", "", err
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("创建会话失败: %v", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// 用bash执行，支持管道/复杂命令
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Run(command)
	}()

	select {
	case <-ctx.Done():
		session.Close()
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("命令执行超时(%v)", timeout)
	case err := <-errCh:
		return stdoutBuf.String(), stderrBuf.String(), err
	}
}
