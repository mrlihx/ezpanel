package waf

// BuiltinRule 表示一条内置安全检测规则。
type BuiltinRule struct {
	ID          string `json:"id"`
	Group       string `json:"group"`      // sensitive | sqli | xss | rce | traversal | scanner | upload
	GroupLabel  string `json:"groupLabel"` // 中文分组名
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`  // uri | query | args | user_agent | upload
	Pattern     string `json:"pattern"` // PCRE 正则（OpenResty ngx.re.find）
	Enabled     bool   `json:"enabled"`
	Action      string `json:"action"` // block | observe
}

// builtinCatalog 是精选低误报规则库，参考 1Panel / 宝塔常见 Web 攻击防护分组。
var builtinCatalog = []BuiltinRule{
	// 敏感文件与配置泄露
	{
		ID: "sensitive-vcs", Group: "sensitive", GroupLabel: "敏感路径与泄露",
		Name: "版本控制与环境变量文件", Description: "拦截 .git、.svn、.hg、.env 等敏感文件访问",
		Target: "uri", Pattern: `/(?:\.git(?:/|$)|\.svn(?:/|$)|\.hg(?:/|$)|\.env(?:[./]|$)|\.ht(?:access|passwd)$)`,
		Enabled: true, Action: "block",
	},
	{
		ID: "sensitive-backup", Group: "sensitive", GroupLabel: "敏感路径与泄露",
		Name: "备份与数据库转储文件", Description: "拦截常见压缩备份、SQL 导出与临时编辑文件",
		Target: "uri", Pattern: `\.(?:sql|bak|old|orig|swp|swo|dump|tar\.gz|tgz|zip|rar|7z)$`,
		Enabled: true, Action: "block",
	},
	{
		ID: "sensitive-cms", Group: "sensitive", GroupLabel: "敏感路径与泄露",
		Name: "常见配置与敏感入口", Description: "拦截 wp-config.php、phpinfo、actuator 等高危敏感路径",
		Target: "uri", Pattern: `(?:/wp-config\.php|/xmlrpc\.php|/phpinfo\.php|/\.DS_Store|/actuator(?:/env|/heapdump|/mappings)?)`,
		Enabled: true, Action: "block",
	},
	// SQL 注入
	{
		ID: "sqli-union", Group: "sqli", GroupLabel: "SQL 注入",
		Name: "UNION SELECT 联合查询注入", Description: "检测 URL 或查询参数中的 UNION SELECT 语法",
		Target: "args", Pattern: `\bunion\b[\s/+*]+(?:all[\s/+*]+)?\bselect\b`,
		Enabled: true, Action: "block",
	},
	{
		ID: "sqli-func", Group: "sqli", GroupLabel: "SQL 注入",
		Name: "高危 SQL 函数与盲注", Description: "检测 sleep()、benchmark()、extractvalue()、information_schema 等注入特征",
		Target: "args", Pattern: `(?:information_schema\b|extractvalue\s*\(|updatexml\s*\(|benchmark\s*\(|sleep\s*\(\s*\d+\s*\)|waitfor\s+delay\b|load_file\s*\(|into\s+(?:out|dump)file\b)`,
		Enabled: true, Action: "block",
	},
	{
		ID: "sqli-boolean", Group: "sqli", GroupLabel: "SQL 注入",
		Name: "布尔永真与堆叠语句注入", Description: "检测常见引号闭合叠加 OR/AND 永真条件或分号堆叠 DDL/DML",
		Target: "args", Pattern: `(?:['"]\s*(?:or|and)\s+['"]?\d+['"]?\s*=\s*['"]?\d+|;\s*(?:drop|alter|truncate|delete|insert|update)\s+(?:table|database|from|into)\b)`,
		Enabled: true, Action: "block",
	},
	// XSS 跨站脚本
	{
		ID: "xss-script", Group: "xss", GroupLabel: "XSS 跨站脚本",
		Name: "Script 标签与伪协议", Description: "检测 <script>、javascript:、vbscript: 等脚本注入载体",
		Target: "args", Pattern: `(?:<\s*script\b|</\s*script\s*>|\bjavascript\s*:|\bvbscript\s*:)`,
		Enabled: true, Action: "block",
	},
	{
		ID: "xss-event", Group: "xss", GroupLabel: "XSS 跨站脚本",
		Name: "HTML 事件处理器注入", Description: "检测 onerror/onload/onclick/onmouseover 等内联事件脚本",
		Target: "args", Pattern: `<\s*(?:img|svg|iframe|body|input|details|marquee|object|embed)\b[^>]*\bon[a-z]+\s*=`,
		Enabled: true, Action: "block",
	},
	// 命令注入与代码执行
	{
		ID: "rce-shell", Group: "rce", GroupLabel: "命令注入与 RCE",
		Name: "系统命令拼接执行", Description: "检测管道或子命令中常见的 shell 命令与反弹特征",
		Target: "args", Pattern: `(?:[;|` + "`" + `]\s*(?:cat|id|whoami|uname|wget|curl|nc|bash|sh|python3?|perl|php)\b|\$\(\s*(?:cat|id|whoami|wget|curl|bash|sh)\b)`,
		Enabled: true, Action: "block",
	},
	{
		ID: "rce-php-jndi", Group: "rce", GroupLabel: "命令注入与 RCE",
		Name: "PHP 危险函数与 JNDI 注入", Description: "检测 eval/base64_decode/system 及 Log4j ${jndi:...} 载荷",
		Target: "args", Pattern: `(?:\$\{\s*jndi\s*:|(?:eval|assert|system|passthru|shell_exec|popen|proc_open)\s*\(|php://(?:input|filter))`,
		Enabled: true, Action: "block",
	},
	// 目录穿越
	{
		ID: "traversal-dotdot", Group: "traversal", GroupLabel: "目录穿越",
		Name: "路径穿越与系统敏感文件读取", Description: "拦截 ../ 穿越序列及 /etc/passwd、/proc/self/environ 等读取尝试",
		Target: "args", Pattern: `(?:\.\./\.\./|%2e%2e(?:/|%2f)|/etc/(?:passwd|shadow|hosts)|/proc/self/environ|win\.ini)`,
		Enabled: true, Action: "block",
	},
	// 恶意扫描器与爬虫
	{
		ID: "scanner-ua", Group: "scanner", GroupLabel: "恶意扫描器",
		Name: "漏洞扫描器与自动化攻击工具 UA", Description: "拦截 sqlmap、nikto、nmap、masscan、dirbuster、acunetix、nuclei 等扫描器",
		Target: "user_agent", Pattern: `(?:sqlmap|nikto|nmap|masscan|dirbuster|gobuster|ffuf|wfuzz|acunetix|netsparker|appscan|nessus|openvas|nuclei|zgrab|httpx|hydra|havij)`,
		Enabled: true, Action: "block",
	},
	// 危险文件上传 / 动态脚本执行
	{
		ID: "upload-webshell", Group: "upload", GroupLabel: "高危脚本与上传",
		Name: "静态目录动态脚本探针", Description: "拦截 uploads/static/images 等静态资源目录下的可执行脚本请求",
		Target: "uri", Pattern: `/(?:uploads?|static|assets|images?|media|files?)/.*\.(?:php[3-8]?|phtml|phar|jsp|jspx|asp|aspx|cgi|sh)$`,
		Enabled: true, Action: "block",
	},
}

func defaultBuiltinRules() []BuiltinRule {
	out := make([]BuiltinRule, len(builtinCatalog))
	copy(out, builtinCatalog)
	return out
}

func ruleGroupLabel(group string) string {
	switch group {
	case "sensitive":
		return "敏感路径与泄露"
	case "sqli":
		return "SQL 注入"
	case "xss":
		return "XSS 跨站脚本"
	case "rce":
		return "命令注入与 RCE"
	case "traversal":
		return "目录穿越"
	case "scanner":
		return "恶意扫描器"
	case "upload":
		return "高危脚本与上传"
	case "cc":
		return "CC 频率限制"
	case "ip_deny":
		return "IP 黑名单"
	case "ban":
		return "动态封禁"
	case "path":
		return "路径拦截"
	case "custom":
		return "自定义规则"
	default:
		return group
	}
}
