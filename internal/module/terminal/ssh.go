package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"ezpanel/internal/store"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

func (m *Module) startSSH(ctx context.Context, userID uint, id string, cols, rows int) (terminalIO, error) {
	var terminal terminalIO
	hostKey := hostPrefix(userID) + id
	host, err := loadHost(m.db.WithContext(ctx), hostKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return terminal, errors.New("SSH 主机不存在")
	}
	if err != nil {
		return terminal, errors.New("无法读取 SSH 主机配置")
	}
	plain, err := store.Decrypt(m.key, host.Secret)
	var credential hostCredential
	if err != nil || json.Unmarshal([]byte(plain), &credential) != nil {
		return terminal, errors.New("无法读取 SSH 凭据")
	}
	var method ssh.AuthMethod
	if host.AuthType == "privateKey" {
		signer, err := credential.signer()
		if err != nil {
			return terminal, errors.New("无法读取 SSH 私钥")
		}
		method = ssh.PublicKeys(signer)
	} else {
		method = ssh.Password(credential.Password)
	}
	var actualFingerprint string
	config := &ssh.ClientConfig{
		User: host.Username, Auth: []ssh.AuthMethod{method},
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actualFingerprint = ssh.FingerprintSHA256(key)
			return nil
		},
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	address := net.JoinHostPort(host.Address, strconv.Itoa(host.Port))
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return terminal, errors.New("无法连接 SSH 服务器，请检查地址和端口")
	}
	stop := context.AfterFunc(dialCtx, func() { _ = conn.Close() })
	defer stop()
	transport, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		return terminal, errors.New("SSH 握手或认证失败")
	}
	var warning string
	if actualFingerprint != "" && actualFingerprint != host.Fingerprint {
		if host.Fingerprint != "" {
			warning = fmt.Sprintf("远程主机「%s」的 SSH 公钥指纹已变更（原 %s → 新 %s），已自动更新记录，请确认是否为预期变更。", host.Name, host.Fingerprint, actualFingerprint)
		}
		host.Fingerprint = actualFingerprint
		if raw, err := json.Marshal(host); err == nil {
			_ = m.db.WithContext(ctx).Model(&store.Setting{Key: hostKey}).Update("value", string(raw)).Error
		}
	}
	client := ssh.NewClient(transport, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return terminal, errors.New("无法打开 SSH 会话")
	}
	input, err := session.StdinPipe()
	if err != nil {
		client.Close()
		return terminal, errors.New("无法打开 SSH 输入")
	}
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		client.Close()
		return terminal, errors.New("远程服务器拒绝分配终端")
	}
	output, writer := io.Pipe()
	session.Stdout, session.Stderr = writer, writer
	if err := session.Shell(); err != nil {
		output.Close()
		writer.Close()
		client.Close()
		return terminal, errors.New("无法启动远程 Shell")
	}
	done := make(chan struct{})
	exitCode := 0
	go func() {
		if err := session.Wait(); err != nil {
			exitCode = -1
			var exit *ssh.ExitError
			if errors.As(err, &exit) {
				exitCode = exit.ExitStatus()
			}
		}
		writer.Close()
		close(done)
	}()

	return terminalIO{
		input: input, output: output, done: done, warning: warning,
		resize:   func(cols, rows uint16) error { return session.WindowChange(int(rows), int(cols)) },
		close:    func() { output.Close(); client.Close(); <-done },
		exitCode: func() int { return exitCode },
	}, nil
}
