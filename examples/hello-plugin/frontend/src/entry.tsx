import { lazy, Suspense, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Flex,
  Spin,
  Typography,
} from "antd";
import { CloudServerOutlined } from "@ant-design/icons";
import type { PanelSDK } from "../../../../sdk/ts/panel-sdk";
import "./style.css";
const Details = lazy(() => import("./details"));
export default function HelloPlugin({ sdk }: { sdk: PanelSDK }) {
  const [info, setInfo] = useState<{
    user: string;
    hostname: string;
    arch: string;
  }>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void sdk
      .api<{ user: string; hostname: string; arch: string }>("/info", {
        signal: controller.signal,
      })
      .then(setInfo)
      .catch((e: unknown) => {
        if (!controller.signal.aborted)
          setError(e instanceof Error ? e.message : "请求失败");
      });
    return () => controller.abort();
  }, [sdk, attempt]);
  return (
    <Card
      title={
        <Flex align="center" gap="small">
          <CloudServerOutlined />
          Hello 插件
        </Flex>
      }
    >
      <Typography.Paragraph>
        共享面板 React、Ant Design 和主题；当前为{sdk.dark ? "深色" : "浅色"}
        主题。
      </Typography.Paragraph>
      {error && <Alert type="error" message={error} />}
      {info ? (
        <Descriptions
          column={1}
          items={[
            { key: "user", label: "管理员", children: info.user },
            { key: "host", label: "服务器", children: info.hostname },
            { key: "arch", label: "架构", children: info.arch },
          ]}
        />
      ) : (
        <Spin />
      )}
      <Button
        onClick={() => {
          setError("");
          setAttempt((n) => n + 1);
          sdk.message.info("通过主应用消息实例刷新");
        }}
      >
        刷新信息
      </Button>
      <Suspense fallback={<Spin />}>
        <Details />
      </Suspense>
    </Card>
  );
}
// 订阅和计时器应在 effect 清理；本例没有模块级常驻资源。
export function dispose() {}
