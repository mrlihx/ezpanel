package pkgmgr

func appendProjectRegistry(args []string, kind, registry string) []string {
	if registry == "" {
		return args
	}
	switch kind {
	case "python":
		return append(args, "-e", "PIP_INDEX_URL="+registry)
	case "nodejs":
		return append(args,
			"-e", "npm_config_registry="+registry,
			"-e", "pnpm_config_registry="+registry,
			"-e", "YARN_NPM_REGISTRY_SERVER="+registry)
	default:
		return args
	}
}
