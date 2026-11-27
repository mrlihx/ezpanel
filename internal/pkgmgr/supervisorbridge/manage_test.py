import os
import pwd
import tempfile
import unittest
from unittest.mock import Mock

from manage import Manager, read_text, replace_section


class SupervisorConfigTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = self.temp.name
        self.main = os.path.join(self.root, "supervisord.conf")
        with open(self.main, "w") as file:
            file.write("[supervisord]\nlogfile=" + self.root + "/main.log\n")
        self.manager = Manager(self.main, self.root)
        rpc = Mock()
        rpc.getAllProcessInfo.return_value = []
        self.manager.rpc = Mock(return_value=rpc)
        self.manager.update = Mock()
        self.values = {"user": pwd.getpwuid(os.geteuid()).pw_name,
                       "directory": self.root, "command": "/bin/echo hello",
                       "numprocs": 2, "priority": 999, "autostart": True,
                       "autorestart": "unexpected", "remark": "测试任务"}

    def create(self):
        self.manager.save({"name": "worker", "values": self.values})
        return self.manager.snapshot()["list"][0]

    def test_create_and_multi_process_logs(self):
        row = self.create()
        self.assertEqual(row["numprocs"], 2)
        self.assertEqual(row["remark"], "测试任务")
        self.assertEqual([item["name"] for item in row["processes"]], ["worker_00", "worker_01"])
        self.assertEqual(row["processes"][1]["stderr"], os.path.join(self.root, "log/supervisor/programs/worker/stderr-01.log"))
        self.manager.update.assert_called_once_with("worker")

    def test_visual_edit_preserves_advanced_options(self):
        row = self.create()
        content = row["content"] + 'environment=MODE="production"\nstartsecs=5\n'
        self.manager.save({"name": "worker", "revision": row["revision"], "content": content})
        row = self.manager.snapshot()["list"][0]
        self.manager.save({"name": "worker", "revision": row["revision"], "values": {**self.values, "remark": "已修改"}})
        row = self.manager.snapshot()["list"][0]
        self.assertIn('environment = MODE="production"', row["content"])
        self.assertIn("startsecs = 5", row["content"])

    def test_invalid_config_rolls_back_without_update(self):
        row = self.create()
        old = read_text(row["path"])
        self.manager.update.reset_mock()
        with self.assertRaises(ValueError):
            self.manager.save({"name": "worker", "revision": row["revision"], "content": "[program:worker]\nnumprocs=invalid\n"})
        self.assertEqual(read_text(row["path"]), old)
        self.manager.update.assert_not_called()

    def test_failed_update_restores_original_config(self):
        row = self.create()
        self.manager.update.side_effect = [ValueError("update failed"), None]
        with self.assertRaisesRegex(ValueError, "已恢复原配置"):
            self.manager.save({"name": "worker", "revision": row["revision"], "values": {**self.values, "command": "/bin/echo changed"}})
        self.assertEqual(read_text(row["path"]), row["content"].lstrip("\n"))
        self.assertEqual(self.manager.snapshot()["list"][0]["command"], "/bin/echo hello")

    def test_rejects_stale_revision_and_extra_section(self):
        row = self.create()
        with self.assertRaisesRegex(ValueError, "已被修改"):
            self.manager.save({"name": "worker", "values": self.values})
        with self.assertRaisesRegex(ValueError, "仅允许"):
            self.manager.save({"name": "worker", "revision": row["revision"], "content": row["content"] + "\n[program:other]\ncommand=/bin/echo other\n"})

    def test_delete_keeps_other_program_and_logs(self):
        row = self.create()
        other = "[program:other]\ncommand=/bin/echo other\n"
        with open(row["path"], "a") as file:
            file.write(other)
        row = next(item for item in self.manager.snapshot()["list"] if item["name"] == "worker")
        logs = os.path.join(self.root, "log/supervisor/programs/worker/stdout-00.log")
        with open(logs, "w") as file:
            file.write("history")
        self.manager.action({"name": "worker", "action": "delete", "revision": row["revision"]})
        self.assertEqual(read_text(row["path"]), other)
        self.assertEqual(read_text(logs), "history")

    def test_initialize_log_directory(self):
        self.manager.initialize()
        self.assertEqual(self.manager.snapshot()["mainLog"], os.path.join(self.root, "log/supervisor/supervisord.log"))

    def test_section_replacement_preserves_surrounding_content(self):
        text = "; keep\n[supervisord]\nlogfile=/tmp/main.log\n\n[include]\nfiles=/tmp/*.conf\n"
        self.assertEqual(replace_section(text, "include", "[include]\nfiles=/new/*.conf\n"),
                         "; keep\n[supervisord]\nlogfile=/tmp/main.log\n\n[include]\nfiles=/new/*.conf\n")


if __name__ == "__main__":
    unittest.main()
