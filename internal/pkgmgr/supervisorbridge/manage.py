"""通过 Supervisor 自带的解析器与本机控制接口管理进程。由 Go 嵌入执行。"""
import glob
import hashlib
import io
import json
import os
import pwd
import re
import socket
import shutil
import sys
import tempfile
import xmlrpc.client

from supervisor.options import ClientOptions, ServerOptions, UnhosedConfigParser
from supervisor.xmlrpc import SupervisorTransport

socket.setdefaulttimeout(50)


def read_text(path):
    with open(path, encoding="utf-8") as file:
        return file.read()


def atomic_write(path, content):
    directory = os.path.dirname(path)
    os.makedirs(directory, mode=0o750, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".ezpanel-", dir=directory)
    try:
        if os.path.exists(path):
            info = os.stat(path)
            os.fchmod(fd, info.st_mode & 0o777)
            os.fchown(fd, info.st_uid, info.st_gid)
        else:
            os.fchmod(fd, 0o640)
        with os.fdopen(fd, "w", encoding="utf-8") as file:
            file.write(content)
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def replace_section(content, section, replacement):
    pattern = re.compile(r"(?m)^\[([^\]\n]+)\][^\n]*(?:\n|$)")
    headers = list(pattern.finditer(content))
    for index, match in enumerate(headers):
        if match.group(1) == section:
            end = headers[index + 1].start() if index + 1 < len(headers) else len(content)
            return content[:match.start()] + replacement + content[end:]
    return (content.rstrip() + "\n\n" if content.strip() else "") + replacement


def section_text(content, section):
    headers = list(re.finditer(r"(?m)^\[([^\]\n]+)\][^\n]*(?:\n|$)", content))
    for index, match in enumerate(headers):
        if match.group(1) == section:
            end = headers[index + 1].start() if index + 1 < len(headers) else len(content)
            return content[match.start():end]
    raise ValueError("进程配置段不存在")


class Manager:
    def __init__(self, main, root):
        self.main = os.path.realpath(main)
        self.root = root
        self.confdir = os.path.realpath(os.path.join(root, "apps", "supervisor", "conf.d"))
        self.logdir = os.path.join(root, "log", "supervisor")

    def options(self):
        options = ServerOptions()
        options.configfile = self.main
        options.process_config(do_usage=False)
        return options

    def parser(self):
        parser = UnhosedConfigParser()
        parser.expansions = {"ENV_" + key: value for key, value in os.environ.items()}
        parser.read(self.main)
        parser.expand_here(os.path.dirname(self.main))
        if parser.has_section("include"):
            patterns = parser.saneget("include", "files", expansions={"here": os.path.dirname(self.main)})
            for pattern in patterns.split():
                for path in sorted(glob.glob(os.path.join(os.path.dirname(self.main), pattern))):
                    parser.read(path)
            parser.expand_here(os.path.dirname(self.main))
        return parser

    def rpc(self):
        options = ClientOptions()
        options.configfile = self.main
        options.process_config(do_usage=False)
        transport = SupervisorTransport(options.username, options.password, options.serverurl)
        return xmlrpc.client.ServerProxy("http://localhost/RPC2", transport=transport).supervisor

    def snapshot(self):
        parser, options = self.parser(), self.options()
        warning = ""
        try:
            infos = self.rpc().getAllProcessInfo()
        except (OSError, xmlrpc.client.Error) as error:
            infos = []
            warning = "无法连接 Supervisor，启动服务后可管理进程。" + str(error)
        info_map = {(item["group"], item["name"]): item for item in infos}
        groups = {}
        for section in parser.sections():
            if section.startswith("group:"):
                for name in parser.get(section, "programs").split(","):
                    groups[name.strip()] = section[6:]
        rows = []
        for section in parser.sections():
            if not section.startswith("program:"):
                continue
            name = section[8:]
            group = groups.get(name, name)
            configs = options.processes_from_section(parser, section, group)
            source = os.path.realpath(parser.section_to_file.get(section, self.main))
            content = section_text(read_text(source), section)
            processes = []
            for process in configs:
                info = info_map.get((group, process.name), {})
                processes.append({
                    "name": process.name, "pid": info.get("pid", 0),
                    "state": info.get("statename", "UNKNOWN"),
                    "description": info.get("spawnerr") or info.get("description", ""),
                    "stdout": info.get("stdout_logfile") or (process.stdout_logfile if isinstance(process.stdout_logfile, str) else ""),
                    "stderr": info.get("stderr_logfile") or (process.stderr_logfile if isinstance(process.stderr_logfile, str) else ""),
                })
            values = dict(parser.items(section))
            rows.append({
                "name": name, "group": group, "command": values.get("command", ""),
                "user": values.get("user", pwd.getpwuid(os.geteuid()).pw_name),
                "directory": configs[0].directory or "", "numprocs": len(configs),
                "priority": configs[0].priority, "autostart": configs[0].autostart,
                "autorestart": values.get("autorestart", "unexpected"),
                "remark": values.get("ezpanel_remark", ""), "path": source,
                "content": content, "revision": hashlib.sha256(content.encode()).hexdigest(),
                "processes": processes,
                "editable": group == name,
            })
        return {"list": rows, "warning": warning, "users": sorted({item.pw_name for item in pwd.getpwall()}),
                "mainLog": options.configroot.supervisord.logfile}

    def initialize(self):
        content = read_text(self.main)
        parser = UnhosedConfigParser()
        parser.read_string(content)
        os.makedirs(self.logdir, mode=0o750, exist_ok=True)
        os.makedirs(self.confdir, mode=0o750, exist_ok=True)
        parser.set("supervisord", "logfile", os.path.join(self.logdir, "supervisord.log"))
        parser.set("supervisord", "logfile_maxbytes", "10MB")
        parser.set("supervisord", "logfile_backups", "5")
        output = io.StringIO()
        selected = UnhosedConfigParser()
        selected.add_section("supervisord")
        for key, value in parser.items("supervisord"):
            selected.set("supervisord", key, value)
        selected.write(output)
        next_content = replace_section(content, "supervisord", output.getvalue())
        includes = parser.get("include", "files", fallback="").split()
        pattern = os.path.join(self.confdir, "*.conf")
        if pattern not in includes:
            includes.append(pattern)
            next_content = replace_section(next_content, "include", "[include]\nfiles = " + " ".join(includes) + "\n\n")
        atomic_write(self.main, next_content)
        try:
            self.options()
        except Exception:
            atomic_write(self.main, content)
            raise

    def update(self, name):
        rpc = self.rpc()
        added, changed, removed = rpc.reloadConfig()[0]
        if name in changed or name in removed:
            results = rpc.stopProcessGroup(name, True)
            failures = [result for result in results if result["status"] not in (70, 80)]
            if failures:
                raise ValueError("停止进程失败：" + str(failures))
            rpc.removeProcessGroup(name)
        if name in changed or name in added:
            rpc.addProcessGroup(name)

    def save(self, data):
        name = data.get("name", "")
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,80}", name):
            raise ValueError("名称须为 1 至 80 位字母、数字、下划线或短横线")
        snapshot = self.snapshot()
        row = next((item for item in snapshot["list"] if item["name"] == name), None)
        if row and not row["editable"]:
            raise ValueError("此进程属于自定义进程组，请在主配置文件中修改")
        if row and data.get("revision") != row["revision"]:
            raise ValueError("配置已被修改，请重新打开后保存")
        if not row and data.get("revision"):
            raise ValueError("进程已被删除，请刷新列表")
        section = "program:" + name
        if "content" in data:
            content = data["content"]
            if not isinstance(content, str) or len(content.encode()) > 131072:
                raise ValueError("配置文件最大为 128 KiB")
            parser = UnhosedConfigParser()
            parser.read_string(content)
            if parser.sections() != [section]:
                raise ValueError("仅允许编辑当前进程的 [program:" + name + "] 配置")
        else:
            values = data.get("values", {})
            for key in ("command", "user", "directory", "remark"):
                value = values.get(key)
                if not isinstance(value, str) or len(value) > 8192 or any(char in value for char in "\r\n\x00"):
                    raise ValueError("进程字段格式无效")
            if not values["command"].strip():
                raise ValueError("请填写启动命令")
            pwd.getpwnam(values["user"])
            if not os.path.isabs(values["directory"]) or not os.path.isdir(values["directory"]):
                raise ValueError("运行目录必须是已存在的绝对路径")
            if type(values.get("numprocs")) is not int or not 1 <= values["numprocs"] <= 100:
                raise ValueError("进程数量须为 1 至 100")
            if type(values.get("priority")) is not int or not 0 <= values["priority"] <= 9999:
                raise ValueError("优先级须为 0 至 9999")
            if type(values.get("autostart")) is not bool or values.get("autorestart") not in ("true", "false", "unexpected"):
                raise ValueError("启动或重启策略无效")
            parser = UnhosedConfigParser()
            if row:
                parser.read_string(row["content"])
            else:
                parser.add_section(section)
                parser.set(section, "stopasgroup", "true")
                parser.set(section, "killasgroup", "true")
            for key in ("command", "user", "directory", "numprocs", "priority", "autorestart"):
                parser.set(section, key, str(values[key]))
            parser.set(section, "ezpanel_remark", values["remark"])
            parser.set(section, "autostart", str(values["autostart"]).lower())
            if values["numprocs"] > 1 and "%(process_num)" not in parser.get(section, "process_name", fallback=""):
                parser.set(section, "process_name", "%(program_name)s_%(process_num)02d")
            logs = os.path.join(self.logdir, "programs", name)
            os.makedirs(logs, mode=0o750, exist_ok=True)
            parser.set(section, "redirect_stderr", "false")
            for kind in ("stdout", "stderr"):
                parser.set(section, kind + "_logfile", os.path.join(logs, kind + "-%(process_num)02d.log"))
                parser.set(section, kind + "_logfile_maxbytes", "10MB")
                parser.set(section, kind + "_logfile_backups", "5")
            output = io.StringIO()
            parser.write(output)
            content = output.getvalue()
        path = row["path"] if row else os.path.join(self.confdir, name + ".conf")
        old = read_text(path) if os.path.exists(path) else None
        main_old = read_text(self.main)
        main_next = main_old
        if not row:
            parser = UnhosedConfigParser()
            parser.read_string(main_old)
            pattern = os.path.join(self.confdir, "*.conf")
            includes = parser.get("include", "files", fallback="").split()
            if pattern not in includes:
                includes.append(pattern)
                main_next = replace_section(main_old, "include", "[include]\nfiles = " + " ".join(includes) + "\n\n")
        # 校验失败恢复磁盘配置；更新只针对当前进程组。
        try:
            atomic_write(path, replace_section(old or "", section, content))
            if main_next != main_old:
                atomic_write(self.main, main_next)
            self.options()
        except Exception:
            self.restore(path, old, main_old, main_next)
            raise
        try:
            self.update(name)
        except Exception as error:
            self.restore(path, old, main_old, main_next)
            try:
                self.update(name)
            except Exception as restore_error:
                raise ValueError("更新失败，原配置已恢复，但进程状态恢复失败：" + str(restore_error)) from error
            raise ValueError("更新失败，已恢复原配置：" + str(error)) from error
        return {"name": name}

    def restore(self, path, old, main_old, main_next):
        if old is None:
            try:
                os.unlink(path)
            except FileNotFoundError:
                pass
        else:
            atomic_write(path, old)
        if main_next != main_old:
            atomic_write(self.main, main_old)

    def action(self, data):
        row = next((item for item in self.snapshot()["list"] if item["name"] == data.get("name")), None)
        if row is None:
            raise ValueError("进程不存在，请刷新列表")
        action = data.get("action")
        if action in ("start", "stop", "restart"):
            rpc = self.rpc()
            failures = []
            for process in row["processes"]:
                target = row["group"] + ":" + process["name"]
                if action in ("stop", "restart") and process["state"] in ("RUNNING", "STARTING", "BACKOFF"):
                    try:
                        rpc.stopProcess(target, True)
                    except xmlrpc.client.Fault as error:
                        if "NOT_RUNNING" not in str(error):
                            failures.append(process["name"] + " " + str(error))
                if action in ("start", "restart") and (action == "restart" or process["state"] != "RUNNING"):
                    try:
                        rpc.startProcess(target, True)
                    except xmlrpc.client.Fault as error:
                        failures.append(process["name"] + " " + str(error))
            if failures:
                raise ValueError("进程启动失败：" + "; ".join(failures))
            return None
        if action != "delete" or not row["editable"]:
            raise ValueError("进程操作无效")
        if data.get("revision") != row["revision"]:
            raise ValueError("配置已被修改，请刷新后再删除")
        path, old = row["path"], read_text(row["path"])
        next_content = replace_section(old, "program:" + row["name"], "")
        try:
            atomic_write(path, next_content)
            self.options()
            self.update(row["name"])
        except Exception:
            atomic_write(path, old)
            try:
                self.update(row["name"])
            except Exception:
                pass
            raise
        if not next_content.strip() and os.path.dirname(path) == self.confdir:
            os.unlink(path)
        if data.get("removeLogs") is True:
            logs = os.path.join(self.logdir, "programs", row["name"])
            if re.fullmatch(r"[A-Za-z0-9_-]{1,80}", row["name"]) and os.path.isdir(logs):
                shutil.rmtree(logs)
        return None


def main():
    manager = Manager(sys.argv[1], sys.argv[2])
    action = sys.argv[3]
    data = json.load(sys.stdin)
    if action == "list":
        result = manager.snapshot()
    elif action == "save":
        result = manager.save(data)
    elif action == "action":
        result = manager.action(data)
    elif action == "initialize":
        manager.initialize()
        result = None
    elif action == "validate":
        manager.options()
        result = None
    else:
        raise ValueError("Supervisor 操作无效")
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
