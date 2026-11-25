package pkgmgr

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// 直接访问本机FastCGI，不给Nginx增加公开状态地址。
func FastCGI(ctx context.Context, network, endpoint string, fields []string, input io.Reader, output io.Writer) error {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, endpoint)
	if err != nil {
		return errors.New("无法连接 PHP-FPM")
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	write := func(kind byte, data []byte) error {
		header := []byte{1, kind, 0, 1, byte(len(data) >> 8), byte(len(data)), 0, 0}
		_, e := io.Copy(conn, io.MultiReader(bytes.NewReader(header), bytes.NewReader(data)))
		return e
	}
	if err = write(1, []byte{0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	params := bytes.Buffer{}
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		for _, n := range []int{len(key), len(value)} {
			if n < 128 {
				params.WriteByte(byte(n))
			} else {
				_ = binary.Write(&params, binary.BigEndian, uint32(n)|0x80000000)
			}
		}
		params.WriteString(key)
		params.WriteString(value)
	}
	buf := make([]byte, 32768)
	for params.Len() > 0 {
		if err = write(4, params.Next(len(buf))); err != nil {
			return err
		}
	}
	if err = write(4, nil); err != nil {
		return err
	}
	if input != nil {
		for {
			n, readErr := input.Read(buf)
			if n > 0 {
				if err = write(5, buf[:n]); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	if err = write(5, nil); err != nil {
		return err
	}
	for {
		header := make([]byte, 8)
		if _, err = io.ReadFull(conn, header); err != nil {
			return err
		}
		if header[0] != 1 {
			return errors.New("FPM状态响应版本无效")
		}
		body := make([]byte, int(binary.BigEndian.Uint16(header[4:6]))+int(header[6]))
		if _, err = io.ReadFull(conn, body); err != nil {
			return err
		}
		if header[1] == 6 {
			if _, err = output.Write(body[:len(body)-int(header[6])]); err != nil {
				return err
			}
		}
		if header[1] == 3 {
			if len(body) < 8 || body[4] != 0 {
				return errors.New("FPM 请求失败")
			}
			return nil
		}
	}
}

func readFPMStatus(ctx context.Context, network, endpoint, path string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	fields := []string{"GATEWAY_INTERFACE=CGI/1.1", "REQUEST_METHOD=GET", "SCRIPT_NAME=" + path, "SCRIPT_FILENAME=/__ezpanel_invalid_status", "REQUEST_URI=" + path + "?json", "QUERY_STRING=json", "SERVER_PROTOCOL=HTTP/1.1", "REMOTE_ADDR=127.0.0.1", "SERVER_NAME=localhost"}
	if err := FastCGI(ctx, network, endpoint, fields, nil, &output); err != nil {
		return nil, err
	}
	_, body, ok := bytes.Cut(output.Bytes(), []byte("\r\n\r\n"))
	if !ok {
		return nil, errors.New("FPM未返回有效状态，请重载服务后重试")
	}
	values := map[string]any{}
	if json.Unmarshal(body, &values) != nil || values["pool"] == nil {
		return nil, errors.New("FPM状态尚未生效，请重载服务后刷新")
	}
	return values, nil
}
