package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

type blockDevice struct {
	Path        string        `json:"path"`
	Type        string        `json:"type"`
	FSType      string        `json:"fstype"`
	Size        uint64        `json:"size"`
	Model       string        `json:"model"`
	UUID        string        `json:"uuid"`
	Mountpoints []string      `json:"mountpoints"`
	Children    []blockDevice `json:"children,omitempty"`
	System      bool          `json:"system"`
	Mountable   bool          `json:"mountable"`
}

func (m *Module) diskInventory(ctx context.Context) ([]blockDevice, error) {
	out, err := command(ctx, "lsblk", "--json", "--bytes", "--exclude", "7,11", "--output", "NAME,PATH,TYPE,FSTYPE,SIZE,MODEL,UUID,MOUNTPOINTS")
	if err != nil {
		return nil, err
	}
	var tree struct {
		Devices []blockDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		return nil, err
	}
	var system func(blockDevice) bool
	system = func(d blockDevice) bool {
		for _, target := range d.Mountpoints {
			if target == "" {
				continue
			}
			if slices.Contains([]string{"/", "/boot", "/boot/efi", "/efi", "/usr", "/etc", "/var", "/home", "/opt"}, target) ||
				pathsafe.Contains(target, m.installDir) {
				return true
			}
		}
		for _, child := range d.Children {
			if system(child) {
				return true
			}
		}
		return false
	}
	devices := []blockDevice{}
	var appendDevice func(blockDevice, bool, string)
	appendDevice = func(d blockDevice, systemDisk bool, model string) {
		children := d.Children
		d.Children = nil
		d.Model = strings.TrimSpace(d.Model)
		if d.Model == "" {
			d.Model = model
		}
		d.Mountpoints = slices.DeleteFunc(d.Mountpoints, func(value string) bool { return value == "" || value == "[SWAP]" })
		d.System = systemDisk
		d.Mountable = !d.System && slices.Contains([]string{"ext2", "ext3", "ext4", "xfs", "btrfs", "vfat", "exfat", "ntfs", "ntfs3", "f2fs"}, d.FSType)
		if i := slices.IndexFunc(devices, func(existing blockDevice) bool { return existing.Path == d.Path }); i >= 0 {
			devices[i].System = devices[i].System || d.System
			devices[i].Mountable = devices[i].Mountable && !d.System
		} else {
			devices = append(devices, d)
		}
		for _, child := range children {
			appendDevice(child, systemDisk, d.Model)
		}
	}
	for _, device := range tree.Devices {
		appendDevice(device, system(device), "")
	}
	return devices, nil
}

func (m *Module) readDisks(c *gin.Context) {
	devices, err := m.diskInventory(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"list": devices})
}

func (m *Module) changeMount(c *gin.Context) {
	var in struct {
		Action string `json:"action"`
		Device string `json:"device"`
		Target string `json:"target"`
	}
	if !response.Bind(c, &in) {
		return
	}
	ctx := c.Request.Context()
	m.mu.Lock()
	defer m.mu.Unlock()
	devices, err := m.diskInventory(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	idx := slices.IndexFunc(devices, func(d blockDevice) bool { return d.Path == in.Device })
	if idx < 0 {
		response.Fail(c, response.NotFound, "磁盘设备不存在")
		return
	}
	device := devices[idx]
	if device.System {
		response.Fail(c, response.Forbidden, "系统磁盘不支持更改挂载状态")
		return
	}
	switch in.Action {
	case "mount":
		if !device.Mountable || len(device.Mountpoints) > 0 {
			response.Fail(c, response.Invalid, "请选择未挂载且具有可用文件系统的磁盘或分区")
			return
		}
		if err := m.mountDisk(ctx, device.Path, strings.TrimSpace(in.Target)); err != nil {
			fail(c, err)
			return
		}
	case "unmount":
		if !slices.Contains(device.Mountpoints, in.Target) {
			response.Fail(c, response.Invalid, "该挂载点当前未挂载此磁盘")
			return
		}
		if _, err := command(ctx, "umount", "--", in.Target); err != nil {
			fail(c, err)
			return
		}
	default:
		response.Fail(c, response.Invalid, "请选择挂载或卸载")
		return
	}
	response.OK(c, nil)
}

func (m *Module) mountDisk(ctx context.Context, device, requested string) error {
	policy, err := pathsafe.New(m.installDir)
	if err != nil {
		return err
	}
	target, err := policy.Check(requested, true)
	if err != nil || policy.CheckTree(target) != nil {
		return errors.New("请选择有效且未受保护的挂载目录")
	}
	findmnt, err := executor.LookPath("findmnt")
	if err != nil {
		return err
	}
	result, err := executor.Run(ctx, 5*time.Second, findmnt, "--mountpoint", target, "--noheadings")
	if err == nil {
		return errors.New("所选目录已是挂载点")
	}
	if result.ExitCode != 1 {
		return errors.New("无法检查目录挂载状态")
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	dir, err := os.Open(target)
	if err != nil {
		return err
	}
	_, err = dir.Readdirnames(1)
	dir.Close()
	if err != io.EOF {
		return errors.New("挂载目录必须为空")
	}
	_, err = command(ctx, "mount", "--", device, target)
	return err
}
