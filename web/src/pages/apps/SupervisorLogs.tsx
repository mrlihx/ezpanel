import { useState } from 'react';
import { Modal, Select, Tabs } from 'antd';
import { supervisorLogURL, type SupervisorProgram } from '../../api/supervisor';
import SoftwareLogContent from './SoftwareLogContent';
import { appSettingsModalStyles } from './ApplicationSettings';

export default function SupervisorLogs({
  program,
  onClose,
}: {
  program: SupervisorProgram;
  onClose: () => void;
}) {
  const [process, setProcess] = useState(program.processes[0]?.name);
  const [kind, setKind] = useState('stdout');
  return (
    <Modal
      open
      centered
      title={`查看日志 · ${program.name}`}
      width={1000}
      styles={appSettingsModalStyles}
      footer={null}
      onCancel={onClose}
      destroyOnHidden
    >
      {program.processes.length > 1 && (
        <Select
          aria-label="进程"
          value={process}
          onChange={setProcess}
          style={{ width: 240, marginBottom: 12 }}
          options={program.processes.map((row) => ({
            value: row.name,
            label: row.name,
          }))}
        />
      )}
      <Tabs
        activeKey={kind}
        onChange={setKind}
        items={[
          { key: 'stdout', label: '运行日志' },
          { key: 'stderr', label: '错误日志' },
        ]}
      />
      <SoftwareLogContent
        key={`${process}:${kind}`}
        url={supervisorLogURL(program.name, process, kind)}
      />
    </Modal>
  );
}
