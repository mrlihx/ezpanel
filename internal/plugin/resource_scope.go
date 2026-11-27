package plugin

import "slices"

type resourceScope struct {
	Kind, Label string
	Permissions []string
}

var resourceScopes = []resourceScope{
	{"sites", "网站", []string{"sites.read", "sites.logs.read", "sites.restart", "sites.control", "backups.read", "backups.create", "backups.verify", "backups.restore", "certificates.bind"}},
	{"containers", "容器", []string{"containers.read", "containers.logs.read", "containers.metrics.read", "containers.control"}},
	{"databases", "数据库", []string{"databases.read", "databases.backup", "databases.restore"}},
	{"certificates", "证书", []string{"certificates.read", "certificates.bind", "certificates.renew"}},
	{"compose", "Compose 项目", []string{"compose.read", "compose.deploy", "deployment.hooks"}},
	{"apps", "应用", []string{"apps.read", "apps.control"}},
}

func (s resourceScope) declared(m Manifest) bool {
	return slices.ContainsFunc(s.Permissions, func(permission string) bool {
		return slices.Contains(m.Permissions, permission)
	})
}

func (g *capabilityGrant) resourceIDs(kind string) *[]string {
	switch kind {
	case "sites":
		return &g.SiteIDs
	case "containers":
		return &g.ContainerIDs
	case "databases":
		return &g.DatabaseIDs
	case "certificates":
		return &g.CertificateIDs
	case "compose":
		return &g.ComposeNames
	case "apps":
		return &g.AppIDs
	}
	return nil
}

func (g capabilityGrant) allowsResource(kind, id string) bool {
	ids := g.resourceIDs(kind)
	return id != "" && ids != nil && (slices.Contains(g.AllResources, kind) || slices.Contains(*ids, id))
}
