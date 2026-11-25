package file

import (
	"bufio"
	"ezpanel/internal/response"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

func (m *Module) identities(c *gin.Context) {
	groupFile, err := os.Open("/etc/group")
	if err != nil {
		response.Fail(c, response.System, "无法读取系统用户组")
		return
	}
	defer groupFile.Close()
	groups := []string{}
	groupNames := map[string]string{}
	scanner := bufio.NewScanner(groupFile)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 3 || fields[0] == "" || strings.HasPrefix(fields[0], "#") {
			continue
		}
		groups = append(groups, fields[0])
		groupNames[fields[2]] = fields[0]
	}
	if scanner.Err() != nil {
		response.Fail(c, response.System, "无法读取系统用户组")
		return
	}
	passwd, err := os.Open("/etc/passwd")
	if err != nil {
		response.Fail(c, response.System, "无法读取系统账号")
		return
	}
	defer passwd.Close()
	type account struct {
		Name  string `json:"name"`
		Group string `json:"group"`
	}
	users := []account{}
	scanner = bufio.NewScanner(passwd)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 4 || fields[0] == "" || strings.HasPrefix(fields[0], "#") {
			continue
		}
		users = append(users, account{Name: fields[0], Group: groupNames[fields[3]]})
	}
	if scanner.Err() != nil {
		response.Fail(c, response.System, "无法读取系统账号")
		return
	}
	sort.Strings(groups)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	response.OK(c, gin.H{"users": users, "groups": groups})
}
