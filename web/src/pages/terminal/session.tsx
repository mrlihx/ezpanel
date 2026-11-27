import { useCallback, useEffect, useRef, useState } from 'react';
import {
  App,
  Badge,
  Button,
  ConfigProvider,
  Dropdown,
  Flex,
  Grid,
  Input,
  Select,
  Space,
  theme,
} from 'antd';
import { useQueryClient } from '@tanstack/react-query';
import { ListChecks, SendHorizontal } from 'lucide-react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { MONO_FONT, SPACING, TERMINAL_COLORS } from '../../styles/tokens';
import type { SavedCommand } from '../../api/toolbox';

const MOBILE_KEYS: { label: string; data: string; title?: string }[] = [
  { label: 'Tab', data: '\t', title: '自动补全 (Tab)' },
  { label: 'Ctrl+C', data: '\x03', title: '中断进程 (Ctrl+C)' },
  { label: 'Ctrl+D', data: '\x04', title: '退出/EOF (Ctrl+D)' },
  { label: 'Ctrl+Z', data: '\x1a', title: '挂起进程 (Ctrl+Z)' },
  { label: 'Ctrl+L', data: '\x0c', title: '清屏 (Ctrl+L)' },
  { label: 'Esc', data: '\x1b', title: 'Esc' },
  { label: '↑', data: '\x1b[A', title: '上一条历史命令' },
  { label: '↓', data: '\x1b[B', title: '下一条历史命令' },
  { label: '←', data: '\x1b[D', title: '光标左移' },
  { label: '→', data: '\x1b[C', title: '光标右移' },
  { label: '/', data: '/' },
  { label: '-', data: '-' },
  { label: '|', data: '|' },
];

export default function Session({
  cwd,
  hostId,
  active,
  fontSize,
  clearCount,
  initialInput,
  commands,
  getPopupContainer,
}: {
  cwd?: string;
  hostId?: string;
  active: boolean;
  fontSize: number;
  clearCount: number;
  initialInput?: string;
  commands?: SavedCommand[];
  getPopupContainer?: () => HTMLElement;
}) {
  const { notification } = App.useApp();
  const queryClient = useQueryClient();
  const { lg } = Grid.useBreakpoint();
  const isTouchDevice =
    !lg ||
    (typeof window !== 'undefined' &&
      window.matchMedia('(pointer: coarse)').matches);
  const initialSent = useRef(false);
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal>();
  const fit = useRef<FitAddon>();
  const socket = useRef<WebSocket>();
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState('连接中');
  const [disconnected, setDisconnected] = useState(false);
  const [commandDraft, setCommandDraft] = useState('');

  const sendRaw = useCallback((data: string) => {
    const ws = socket.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || !data) return;
    // 按 Unicode 字符切片，JSON 编码后仍低于服务端单帧限制。
    let chunk = '';
    for (const char of data) {
      chunk += char;
      if (chunk.length >= 4096) {
        ws.send(JSON.stringify({ type: 'input', data: chunk }));
        chunk = '';
      }
    }
    if (chunk) ws.send(JSON.stringify({ type: 'input', data: chunk }));
  }, []);

  useEffect(() => {
    const terminal = new Terminal({
      fontFamily: MONO_FONT,
      fontSize: 14,
      theme: TERMINAL_COLORS,
      cursorBlink: true,
    });
    const addon = new FitAddon();
    terminal.loadAddon(addon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(host.current!);

    // 优化移动端输入法 (IME) 兼容性：禁用 xterm 隐藏 textarea 的自动大写、自动纠错与拼写检查，
    // 防止 iOS / Android 中文及英文预测输入法在候选词合成阶段吞字或丢失上屏事件。
    const textarea = terminal.textarea;
    if (textarea) {
      textarea.setAttribute('autocapitalize', 'off');
      textarea.setAttribute('autocomplete', 'off');
      textarea.setAttribute('autocorrect', 'off');
      textarea.setAttribute('spellcheck', 'false');
      textarea.setAttribute('enterkeyhint', 'send');
    }

    const sanitizeCopiedText = (raw: string) =>
      raw
        .replace(/\u00a0/g, ' ')
        // eslint-disable-next-line no-control-regex
        .replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, '');

    const copyToClipboard = (text: string) => {
      const cleaned = sanitizeCopiedText(text);
      if (!cleaned) return;
      const fallbackCopy = () => {
        const el = document.createElement('textarea');
        el.value = cleaned;
        el.setAttribute('readonly', '');
        el.style.position = 'fixed';
        el.style.top = '-9999px';
        el.style.left = '-9999px';
        el.style.opacity = '0';
        document.body.appendChild(el);
        el.focus();
        el.select();
        try {
          document.execCommand('copy');
        } catch {
          // 忽略浏览器不支持的情况
        }
        document.body.removeChild(el);
        terminal.focus();
      };
      if (navigator.clipboard?.writeText) {
        void navigator.clipboard.writeText(cleaned).catch(() => {
          fallbackCopy();
        });
      } else {
        fallbackCopy();
      }
    };

    // 统一处理终端复制/粘贴与中断快捷键（兼容 HTTP/HTTPS、中英文输入法及 macOS/Windows）：
    // - 有选区时：Cmd+C / Ctrl+C / Ctrl+Shift+C 复制到剪贴板并清空选区
    // - 无选区时：Cmd+C（macOS）和 Ctrl+C（Windows/Linux/macOS）均直接发送 \x03 (^C) 中断
    terminal.attachCustomKeyEventHandler((event) => {
      const key = event.key.toLowerCase();
      const isC = event.code === 'KeyC' || key === 'c';
      const isV = event.code === 'KeyV' || key === 'v';
      const hasSelection =
        terminal.hasSelection() && terminal.getSelection().length > 0;
      const isSingleMod =
        (event.metaKey && !event.ctrlKey) || (event.ctrlKey && !event.metaKey);

      if (isC && !event.altKey && isSingleMod) {
        if (event.shiftKey) {
          if (event.type === 'keydown' && hasSelection) {
            event.preventDefault();
            copyToClipboard(terminal.getSelection());
            terminal.clearSelection();
          }
          return false;
        }
        if (event.type === 'keydown') {
          event.preventDefault();
          if (hasSelection) {
            copyToClipboard(terminal.getSelection());
            terminal.clearSelection();
          } else {
            sendRaw('\x03');
          }
        }
        return false;
      }

      if (isV && !event.altKey && isSingleMod) {
        if (
          event.type === 'keydown' &&
          event.shiftKey &&
          navigator.clipboard?.readText
        ) {
          event.preventDefault();
          void navigator.clipboard
            .readText()
            .then((text) => {
              if (text) terminal.paste(text);
            })
            .catch(() => {});
        }
        return false;
      }

      return true;
    });

    const container = host.current!;
    const onCopy = (event: ClipboardEvent) => {
      const selected = terminal.hasSelection()
        ? terminal.getSelection()
        : (window.getSelection()?.toString() ?? '');
      if (!selected) return;
      const cleaned = sanitizeCopiedText(selected);
      if (event.clipboardData) {
        event.preventDefault();
        event.clipboardData.setData('text/plain', cleaned);
      }
    };
    container.addEventListener('copy', onCopy);

    term.current = terminal;
    fit.current = addon;
    return () => {
      container.removeEventListener('copy', onCopy);
      terminal.dispose();
      term.current = undefined;
      fit.current = undefined;
    };
  }, [sendRaw]);

  useEffect(() => {
    const terminal = term.current!;
    let stopped = false;
    const size = fit.current!.proposeDimensions();
    const params = new URLSearchParams({
      cols: String(Math.min(500, Math.max(1, size?.cols || 80))),
      rows: String(Math.min(300, Math.max(1, size?.rows || 24))),
    });
    if (hostId) params.set('hostId', hostId);
    else if (cwd) params.set('cwd', cwd);
    const ws = new WebSocket(
      `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/terminal?${params}`,
    );
    ws.binaryType = 'arraybuffer';
    socket.current = ws;
    const ready = () => {
      if (stopped) return;
      setStatus('已连接');
      if (initialInput && !initialSent.current) {
        initialSent.current = true;
        sendRaw(initialInput + '\n');
      }
      setDisconnected(false);
      if (host.current?.clientWidth) {
        fit.current?.fit();
        terminal.focus();
      }
    };
    if (!hostId) ws.onopen = ready;
    ws.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
      if (stopped) return;
      if (event.data instanceof ArrayBuffer)
        terminal.write(new Uint8Array(event.data));
      else {
        let frame: { type: string; code?: number; warning?: string };
        try {
          frame = JSON.parse(event.data) as {
            type: string;
            code?: number;
            warning?: string;
          };
        } catch {
          return;
        }
        if (frame.type === 'ready') {
          if (frame.warning) {
            terminal.write(`\x1b[33m[安全提示] ${frame.warning}\x1b[0m\r\n`);
            notification.warning({
              message: 'SSH 主机指纹已变更',
              description: frame.warning,
              duration: 10,
            });
          }
          if (hostId) {
            void queryClient.invalidateQueries({
              queryKey: ['terminal', 'hosts'],
            });
          }
          ready();
        } else if (frame.type === 'exit') {
          setStatus(`Shell 已退出，退出码 ${frame.code}`);
          setDisconnected(true);
        }
      }
    };
    ws.onclose = (event) => {
      if (stopped) return;
      setStatus((previous) =>
        previous.startsWith('Shell 已退出')
          ? previous
          : event.reason || '连接已断开',
      );
      setDisconnected(true);
    };
    const input = terminal.onData((data) => {
      sendRaw(data);
    });
    const resize = terminal.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN)
        ws.send(
          JSON.stringify({
            type: 'resize',
            cols: Math.min(500, cols),
            rows: Math.min(300, rows),
          }),
        );
    });
    const ping = setInterval(() => {
      if (ws.readyState === WebSocket.OPEN)
        ws.send(JSON.stringify({ type: 'ping' }));
    }, 30000);
    return () => {
      stopped = true;
      clearInterval(ping);
      input.dispose();
      resize.dispose();
      ws.close();
      socket.current = undefined;
    };
  }, [cwd, hostId, attempt, initialInput, sendRaw, notification, queryClient]);

  useEffect(() => {
    term.current!.options.fontSize = fontSize;
    if (active) {
      fit.current?.fit();
      term.current?.focus();
    }
  }, [fontSize, active]);

  useEffect(() => {
    if (clearCount) term.current?.clear();
  }, [clearCount]);

  useEffect(() => {
    const observer = new ResizeObserver(() => {
      if (active && host.current!.clientWidth && host.current!.clientHeight)
        fit.current?.fit();
    });
    observer.observe(host.current!);
    return () => observer.disconnect();
  }, [active]);

  const submitCommand = () => {
    if (status !== '已连接') return;
    sendRaw(commandDraft + '\r');
    setCommandDraft('');
  };
  const commandOptions = commands?.map(({ name }, index) => ({
    label: name,
    value: index,
  }));

  return (
    <div
      style={{
        display: active ? 'flex' : 'none',
        flex: 1,
        minHeight: 0,
        position: 'relative',
        flexDirection: 'column',
        background: TERMINAL_COLORS.background,
        color: TERMINAL_COLORS.foreground,
        padding: SPACING.md,
        borderRadius: 16,
        overflow: 'hidden',
        boxShadow: '0 8px 28px rgba(0, 0, 0, 0.22)',
      }}
    >
      <Space style={{ marginBottom: SPACING.sm }}>
        <Badge
          status={
            disconnected
              ? 'error'
              : status === '已连接'
                ? 'success'
                : 'processing'
          }
        />
        <span>{status}</span>
      </Space>
      <div
        ref={host}
        onClick={() => {
          // 轻触终端黑屏区域时显式唤起 xterm 隐藏 textarea 焦点
          term.current?.focus();
        }}
        style={{ flex: 1, minHeight: 0, overflow: 'hidden' }}
      />
      {isTouchDevice && (
        <div
          className="terminal-mobile-bar"
          style={{
            marginTop: SPACING.sm,
            paddingTop: SPACING.sm,
            borderTop: '1px solid rgba(255, 255, 255, 0.1)',
            display: 'flex',
            flexDirection: 'column',
            gap: 8,
            flexShrink: 0,
          }}
        >
          <div
            style={{
              display: 'flex',
              gap: 6,
              overflowX: 'auto',
              paddingBottom: 2,
              scrollbarWidth: 'none',
              minWidth: 0,
            }}
          >
            {MOBILE_KEYS.map((item) => (
              <button
                key={item.label}
                type="button"
                title={item.title || item.label}
                disabled={status !== '已连接'}
                onPointerDown={(e) => {
                  // 阻止按钮抢夺当前输入框或终端的软键盘焦点
                  e.preventDefault();
                }}
                onClick={() => sendRaw(item.data)}
                style={{
                  appearance: 'none',
                  border: '1px solid rgba(255, 255, 255, 0.14)',
                  background: 'rgba(255, 255, 255, 0.08)',
                  color: '#F5F5F7',
                  borderRadius: 8,
                  padding: '4px 10px',
                  fontSize: 12,
                  fontFamily: MONO_FONT,
                  fontWeight: 500,
                  whiteSpace: 'nowrap',
                  flexShrink: 0,
                  cursor: 'pointer',
                }}
              >
                {item.label}
              </button>
            ))}
          </div>
          <Flex gap={8} align="center">
            {commands && (
              <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
                <Dropdown
                  trigger={['click']}
                  placement="topLeft"
                  overlayClassName="terminal-command-dropdown"
                  getPopupContainer={getPopupContainer}
                  menu={{
                    items: commandOptions?.length
                      ? commandOptions.map(({ label, value }) => ({
                          label,
                          key: String(value),
                        }))
                      : [
                          {
                            label: '暂无常用命令',
                            key: 'empty',
                            disabled: true,
                          },
                        ],
                    onClick: ({ key }) => {
                      const command = commands[Number(key)];
                      if (command) setCommandDraft(command.command);
                    },
                  }}
                >
                  <button
                    type="button"
                    className="terminal-mobile-cmd-btn"
                    aria-label="选择常用命令"
                    title="选择常用命令"
                    disabled={status !== '已连接'}
                  >
                    <ListChecks size={15} />
                    <span>常用</span>
                  </button>
                </Dropdown>
              </ConfigProvider>
            )}
            <Input
              className="terminal-mobile-input"
              value={commandDraft}
              disabled={status !== '已连接'}
              onChange={(e) => setCommandDraft(e.target.value)}
              onPressEnter={submitCommand}
              placeholder="输入命令，回车或点发送执行…"
              autoCapitalize="off"
              autoCorrect="off"
              autoComplete="off"
              spellCheck={false}
              style={{
                flex: 1,
                fontFamily: MONO_FONT,
                fontSize: 13,
              }}
            />
            <Button
              type="primary"
              disabled={status !== '已连接'}
              icon={<SendHorizontal size={15} strokeWidth={2} />}
              onClick={submitCommand}
            >
              发送
            </Button>
          </Flex>
        </div>
      )}
      {!isTouchDevice && commands && (
        <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
          <Flex className="terminal-command-bar" align="end" gap={SPACING.xs}>
            <Select
              className="terminal-command-select"
              popupClassName="terminal-command-dropdown"
              aria-label="选择常用命令"
              placeholder="常用命令"
              disabled={status !== '已连接'}
              popupMatchSelectWidth={false}
              placement="topLeft"
              getPopupContainer={getPopupContainer}
              notFoundContent="暂无常用命令"
              value={null}
              options={commandOptions}
              onSelect={(index) => {
                if (index !== null) setCommandDraft(commands[index].command);
              }}
            />
            <Input.TextArea
              className="terminal-command-input"
              autoSize={{ minRows: 1, maxRows: 4 }}
              aria-label="终端命令"
              placeholder="输入命令，按回车执行"
              disabled={status !== '已连接'}
              value={commandDraft}
              autoCapitalize="off"
              autoCorrect="off"
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setCommandDraft(event.target.value)}
              onKeyDown={(event) => {
                if (
                  event.key !== 'Enter' ||
                  event.shiftKey ||
                  event.nativeEvent.isComposing
                )
                  return;
                event.preventDefault();
                if (commandDraft.trim()) submitCommand();
              }}
            />
            <Button
              type="primary"
              disabled={status !== '已连接' || !commandDraft.trim()}
              icon={<SendHorizontal size={15} strokeWidth={2} />}
              onClick={submitCommand}
            >
              发送
            </Button>
          </Flex>
        </ConfigProvider>
      )}
      {disconnected && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: TERMINAL_COLORS.overlay,
          }}
        >
          <Space direction="vertical" align="center">
            <span>连接已断开</span>
            <span>{status}</span>
            <Button
              type="primary"
              onClick={() => {
                term.current?.reset();
                setStatus('连接中');
                setDisconnected(false);
                setAttempt(attempt + 1);
              }}
            >
              重新连接
            </Button>
          </Space>
        </div>
      )}
    </div>
  );
}
