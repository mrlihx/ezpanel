import DiskMounts from './DiskMounts';
import type { ToolboxPageState } from './index';

export default function ToolDisksPanel({
  disks,
}: Pick<ToolboxPageState, 'disks'>) {
  return (
    <>
      {disks.data && (
        <DiskMounts list={disks.data.list} onChanged={disks.refetch} />
      )}
    </>
  );
}
