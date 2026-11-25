import { Typography } from "antd";
export default function Details() {
  return (
    <Typography.Paragraph type="secondary">
      此段使用内容哈希的相对动态分块，后端只读取已授予的 sysinfo.read。
    </Typography.Paragraph>
  );
}
