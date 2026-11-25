-- EzPanel SQLite Schema (unified, idempotent)

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE,
  password_hash TEXT,
  auth_version INTEGER DEFAULT 1,
  created_at DATETIME
);

CREATE TABLE IF NOT EXISTS auth_sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  last_active DATETIME NOT NULL,
  expires_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS login_attempts (
  ip TEXT PRIMARY KEY,
  count INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS login_attempts_expiry ON login_attempts(expires_at);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT
);

CREATE TABLE IF NOT EXISTS websites (
  id TEXT PRIMARY KEY,
  name TEXT,
  domains TEXT,
  type TEXT,
  root TEXT,
  root_created_by_panel INTEGER,
  proxy_target TEXT,
  ssl_enabled INTEGER,
  force_https INTEGER,
  enabled INTEGER,
  remark TEXT,
  created_at DATETIME,
  updated_at DATETIME,
  php_version TEXT NOT NULL DEFAULT '',
  rewrite_rule TEXT NOT NULL DEFAULT '',
  redirect_enabled INTEGER NOT NULL DEFAULT 0,
  redirect_target TEXT NOT NULL DEFAULT '',
  redirect_code INTEGER NOT NULL DEFAULT 301,
  redirect_keep_uri INTEGER NOT NULL DEFAULT 1,
  log_directory TEXT NOT NULL DEFAULT '',
  certificate_id TEXT NOT NULL DEFAULT '',
  ssl_allow_mismatch INTEGER NOT NULL DEFAULT 0,
  project_type TEXT NOT NULL DEFAULT 'php',
  runtime_version TEXT NOT NULL DEFAULT '',
  runtime_command TEXT NOT NULL DEFAULT '',
  runtime_port INTEGER NOT NULL DEFAULT 0,
  runtime_env TEXT NOT NULL DEFAULT '',
  runtime_environment TEXT NOT NULL DEFAULT '',
  proxy_routes TEXT NOT NULL DEFAULT '[]',
  runtime_launch TEXT NOT NULL DEFAULT '',
  runtime_entry TEXT NOT NULL DEFAULT '',
  runtime_workers INTEGER NOT NULL DEFAULT 0,
  runtime_threads INTEGER NOT NULL DEFAULT 0,
  runtime_timeout INTEGER NOT NULL DEFAULT 0,
  runtime_file TEXT NOT NULL DEFAULT '',
  runtime_auto_start INTEGER NOT NULL DEFAULT 0,
  runtime_package_manager TEXT NOT NULL DEFAULT '',
  runtime_script TEXT NOT NULL DEFAULT '',
  runtime_requirements TEXT NOT NULL DEFAULT '',
  runtime_pending INTEGER NOT NULL DEFAULT 0,
  waf TEXT NOT NULL DEFAULT '{}',
  runtime_registry TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS cron_jobs (
  id TEXT PRIMARY KEY,
  name TEXT,
  type TEXT,
  schedule TEXT,
  config TEXT,
  keep_count INTEGER,
  enabled INTEGER,
  disabled_reason TEXT,
  last_run_at DATETIME,
  last_status TEXT,
  created_at DATETIME,
  updated_at DATETIME
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  module TEXT,
  action TEXT,
  target TEXT,
  resource_id TEXT,
  cron_job_id TEXT REFERENCES cron_jobs(id) ON DELETE SET NULL,
  status TEXT,
  exit_code INTEGER,
  error_code INTEGER,
  error_msg TEXT,
  result TEXT,
  log_path TEXT,
  log_truncated INTEGER,
  started_at DATETIME,
  finished_at DATETIME,
  log_start INTEGER NOT NULL DEFAULT 0,
  log_size INTEGER
);

CREATE TABLE IF NOT EXISTS db_servers (
  id TEXT PRIMARY KEY,
  name TEXT,
  type TEXT,
  network TEXT,
  host TEXT,
  port INTEGER,
  socket_path TEXT,
  username TEXT,
  password_enc TEXT,
  tls_mode TEXT,
  tls_server_name TEXT,
  tls_ca_path TEXT,
  created_at DATETIME,
  updated_at DATETIME,
  is_local INTEGER NOT NULL DEFAULT 0,
  instance_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS db_metadata (
  server_id TEXT NOT NULL REFERENCES db_servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '',
  password_enc TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (server_id, name)
);

CREATE TABLE IF NOT EXISTS db_backups (
  id TEXT PRIMARY KEY,
  server_id TEXT REFERENCES db_servers(id) ON DELETE SET NULL,
  server_name TEXT,
  "database" TEXT,
  file_path TEXT,
  size INTEGER,
  cron_job_id TEXT REFERENCES cron_jobs(id) ON DELETE SET NULL,
  created_at DATETIME,
  file_name TEXT NOT NULL DEFAULT '',
  storage_type TEXT NOT NULL DEFAULT 'local' CHECK (storage_type IN ('local', 'remote')),
  storage_id TEXT NOT NULL DEFAULT '',
  remote_key TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT 'manual' CHECK (method IN ('manual', 'automatic'))
);

CREATE TABLE IF NOT EXISTS database_trash (
  id TEXT PRIMARY KEY,
  server_id TEXT REFERENCES db_servers(id) ON DELETE SET NULL,
  server_name TEXT NOT NULL,
  database_name TEXT NOT NULL,
  backup_id TEXT NOT NULL REFERENCES db_backups(id),
  charset TEXT NOT NULL,
  collation TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  deleted_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS database_records (
  engine TEXT NOT NULL,
  server_id TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  PRIMARY KEY (engine, server_id, name)
);

CREATE TABLE IF NOT EXISTS mongo_metadata (
  connection_id TEXT NOT NULL,
  name TEXT NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  password_enc TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (connection_id, name)
);

CREATE TABLE IF NOT EXISTS mongo_backups (
  id TEXT PRIMARY KEY,
  connection_id TEXT NOT NULL,
  connection_name TEXT NOT NULL,
  "database" TEXT NOT NULL,
  file_path TEXT NOT NULL,
  size INTEGER NOT NULL,
  created_at DATETIME NOT NULL,
  file_name TEXT NOT NULL,
  storage_type TEXT NOT NULL DEFAULT 'local',
  storage_id TEXT NOT NULL DEFAULT '',
  remote_key TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT 'manual'
);

CREATE TABLE IF NOT EXISTS site_backups (
  id TEXT PRIMARY KEY,
  website_id TEXT REFERENCES websites(id) ON DELETE SET NULL,
  site_name TEXT,
  file_path TEXT,
  size INTEGER,
  cron_job_id TEXT REFERENCES cron_jobs(id) ON DELETE SET NULL,
  created_at DATETIME,
  file_name TEXT NOT NULL DEFAULT '',
  storage_type TEXT NOT NULL DEFAULT 'local' CHECK (storage_type IN ('local', 'remote')),
  storage_id TEXT NOT NULL DEFAULT '',
  remote_key TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT 'manual' CHECK (method IN ('manual', 'automatic'))
);

CREATE TABLE IF NOT EXISTS backup_channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL,
  endpoint TEXT NOT NULL,
  username TEXT NOT NULL,
  secret TEXT NOT NULL,
  fingerprint TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS trash_items (
  id TEXT PRIMARY KEY,
  name TEXT,
  original_path TEXT,
  storage_path TEXT,
  is_dir INTEGER,
  file_type TEXT,
  size INTEGER,
  deleted_at DATETIME
);

CREATE TABLE IF NOT EXISTS file_notes (
  path TEXT PRIMARY KEY,
  remark TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS acme_accounts (
  email TEXT PRIMARY KEY,
  key_enc TEXT NOT NULL,
  uri TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS certificates (
  id TEXT PRIMARY KEY,
  domains TEXT NOT NULL,
  certificate TEXT NOT NULL,
  key_enc TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  issuer TEXT NOT NULL,
  not_before DATETIME NOT NULL,
  not_after DATETIME NOT NULL,
  source TEXT NOT NULL,
  validation_method TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  algorithm TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS certificate_orders (
  id TEXT PRIMARY KEY,
  domains TEXT NOT NULL,
  email TEXT NOT NULL,
  method TEXT NOT NULL,
  site_id TEXT NOT NULL DEFAULT '',
  renewal_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  account_uri TEXT NOT NULL DEFAULT '',
  order_uri TEXT NOT NULL DEFAULT '',
  key_enc TEXT NOT NULL DEFAULT '',
  challenges TEXT NOT NULL DEFAULT '[]',
  certificate_id TEXT NOT NULL DEFAULT '',
  task_id TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  algorithm TEXT NOT NULL DEFAULT 'ecdsa256',
  auto_deploy INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS plugins (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  type TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT 0,
  manifest TEXT NOT NULL,
  config TEXT NOT NULL,
  operation TEXT,
  installed_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS plugin_kv (
  plugin_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (plugin_id, key),
  FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS docker_compose_projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL,
  owned_dir BOOLEAN NOT NULL,
  work_dir TEXT NOT NULL,
  compose_files TEXT NOT NULL,
  env_file TEXT NOT NULL,
  remark TEXT NOT NULL,
  template_id TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS docker_compose_revisions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES docker_compose_projects(id) ON DELETE CASCADE,
  compose_yaml TEXT NOT NULL,
  env_content TEXT NOT NULL,
  had_env BOOLEAN NOT NULL,
  created_by TEXT NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_channels (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL,
  is_default BOOLEAN NOT NULL,
  secret TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_rules (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  enabled BOOLEAN NOT NULL,
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  target_name TEXT NOT NULL,
  interval_seconds INTEGER NOT NULL,
  max_daily INTEGER NOT NULL,
  days INTEGER NOT NULL,
  state TEXT NOT NULL,
  last_checked_at DATETIME,
  next_check_at DATETIME NOT NULL,
  sent_date TEXT NOT NULL,
  sent_today INTEGER NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_rule_channels (
  rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES alert_channels(id) ON DELETE RESTRICT,
  PRIMARY KEY (rule_id, channel_id)
);

CREATE TABLE IF NOT EXISTS alert_delivery_logs (
  id TEXT PRIMARY KEY,
  rule_id TEXT NOT NULL,
  title TEXT NOT NULL,
  channel_name TEXT NOT NULL,
  kind TEXT NOT NULL,
  success BOOLEAN NOT NULL,
  error TEXT NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_delivery_queue (
  id TEXT PRIMARY KEY,
  rule_id TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  channel_name TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS waf_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  data TEXT NOT NULL DEFAULT '{}',
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS waf_builtin_overrides (
  rule_id TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL,
  action TEXT NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS waf_custom_rules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  site_id TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL DEFAULT 100,
  action TEXT NOT NULL DEFAULT 'block',
  match_field TEXT NOT NULL,
  match_operator TEXT NOT NULL,
  match_value TEXT NOT NULL,
  header_name TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS waf_exemptions (
  id TEXT PRIMARY KEY,
  site_id TEXT NOT NULL DEFAULT '',
  rule_id TEXT NOT NULL,
  uri TEXT NOT NULL,
  remark TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS waf_site_policies (
  site_id TEXT PRIMARY KEY,
  inherit_global BOOLEAN NOT NULL DEFAULT 1,
  mode TEXT NOT NULL DEFAULT 'block',
  cc_enabled BOOLEAN NOT NULL DEFAULT 1,
  cc_window_seconds INTEGER NOT NULL DEFAULT 60,
  cc_max_requests INTEGER NOT NULL DEFAULT 120,
  cc_action TEXT NOT NULL DEFAULT 'block',
  allow_ips TEXT NOT NULL DEFAULT '[]',
  deny_ips TEXT NOT NULL DEFAULT '[]',
  allow_paths TEXT NOT NULL DEFAULT '[]',
  deny_paths TEXT NOT NULL DEFAULT '[]',
  disabled_groups TEXT NOT NULL DEFAULT '[]',
  updated_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS alert_channel_default ON alert_channels(kind) WHERE is_default = 1;
CREATE INDEX IF NOT EXISTS alert_delivery_logs_created ON alert_delivery_logs(created_at);
CREATE INDEX IF NOT EXISTS alert_delivery_logs_rule ON alert_delivery_logs(rule_id, created_at);
CREATE INDEX IF NOT EXISTS alert_delivery_queue_created ON alert_delivery_queue(created_at, id);
CREATE INDEX IF NOT EXISTS alert_rule_channels_channel ON alert_rule_channels(channel_id);
CREATE INDEX IF NOT EXISTS alert_rules_due ON alert_rules(enabled, next_check_at);
CREATE INDEX IF NOT EXISTS auth_sessions_expiry ON auth_sessions(expires_at);
CREATE INDEX IF NOT EXISTS docker_compose_revisions_project ON docker_compose_revisions(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_certificate_orders_site_created ON certificate_orders(site_id, created_at);
CREATE INDEX IF NOT EXISTS idx_db_backups_server_id_database_created_at ON db_backups("server_id", "database", "created_at");
CREATE INDEX IF NOT EXISTS idx_mongo_backups_connection_database ON mongo_backups(connection_id, "database", created_at);
CREATE INDEX IF NOT EXISTS idx_site_backups_website_id_created_at ON site_backups("website_id", "created_at");
CREATE INDEX IF NOT EXISTS idx_tasks_cron_job_id_started_at ON tasks("cron_job_id", "started_at");
CREATE INDEX IF NOT EXISTS idx_tasks_module_started_at ON tasks("module", "started_at");
CREATE INDEX IF NOT EXISTS idx_trash_items_deleted_at ON trash_items("deleted_at");
CREATE INDEX IF NOT EXISTS idx_waf_custom_rules_site ON waf_custom_rules(site_id, priority);
CREATE UNIQUE INDEX IF NOT EXISTS idx_waf_exemptions_unique ON waf_exemptions(site_id, rule_id, uri);
