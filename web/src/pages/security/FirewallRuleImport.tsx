import { firewallRuleNames } from './firewallLabels';
import { Button, Input, Modal, Typography, Upload } from 'antd';
import { LAYOUT } from '../../styles/tokens';
import { importFirewallRules, type FirewallRule } from '../../api/security';

import type { FirewallPageState } from './Firewall';

export default function FirewallRuleImport({
  importing,
  json,
  kind,
  message,
  query,
  setImporting,
  setJSON,
  setWorking,
  working,
}: Pick<
  FirewallPageState,
  | 'importing'
  | 'json'
  | 'kind'
  | 'message'
  | 'query'
  | 'setImporting'
  | 'setJSON'
  | 'setWorking'
  | 'working'
>) {
  return (
    <Modal
      open={importing}
      title={`导入${firewallRuleNames[kind]}`}
      width={LAYOUT.shellWidth}
      maskClosable={false}
      confirmLoading={working}
      onCancel={() => setImporting(false)}
      onOk={async () => {
        let data: FirewallRule[];
        try {
          data = JSON.parse(json);
        } catch {
          void message.error('请输入有效JSON数组');
          return;
        }
        if (!Array.isArray(data)) {
          void message.error('规则必须是JSON数组');
          return;
        }
        setWorking(true);
        try {
          await importFirewallRules(kind, data);
          setImporting(false);
          await query.refetch();
          message.success('规则导入完成');
        } finally {
          setWorking(false);
        }
      }}
    >
      <Typography.Paragraph>
        上传本面板导出的对应类型JSON文件，或粘贴规则数组。仅追加当前页面类型的规则。
      </Typography.Paragraph>
      <Upload
        accept=".json"
        showUploadList={false}
        beforeUpload={async (file) => {
          if (file.size > 2 * 1024 * 1024) {
            void message.error('文件不能超过2MiB');
            return false;
          }
          setJSON(await file.text());
          return false;
        }}
      >
        <Button style={{ marginBottom: 12 }}>选择规则文件</Button>
      </Upload>
      <Input.TextArea
        rows={12}
        value={json}
        onChange={(event) => setJSON(event.target.value)}
      />
    </Modal>
  );
}
