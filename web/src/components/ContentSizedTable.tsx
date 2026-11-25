import type { ComponentProps, CSSProperties } from 'react';
import { Table, type TableProps } from 'antd';
import './ContentSizedTable.css';

type Columns<Row extends object> = NonNullable<TableProps<Row>['columns']>;

type Props<Row extends object> = Omit<
  TableProps<Row>,
  'columns' | 'components' | 'tableLayout'
> & {
  columns: Columns<Row>;
  fillColumn: string | string[];
  growColumns?: readonly string[];
  legacySizing?: boolean;
};

function ContentSizedCell({ children, ...props }: ComponentProps<'td'>) {
  return (
    <td {...props}>
      <div className="content-sized-cell">{children}</div>
    </td>
  );
}

export default function ContentSizedTable<Row extends object>({
  columns,
  fillColumn,
  growColumns = [],
  legacySizing = false,
  className,
  scroll,
  style,
  ...props
}: Props<Row>) {
  const preferred = typeof fillColumn === 'string' ? [fillColumn] : fillColumn;
  const fill =
    preferred
      .map((key) =>
        columns.find(
          (column) =>
            column.key === key ||
            column.title === key ||
            ('dataIndex' in column && column.dataIndex === key),
        ),
      )
      .find(Boolean) ?? columns[0];
  const adjusted = columns.map((column) => {
    const width = 'width' in column ? column.width : undefined;
    const actions =
      column.key === 'actions' ||
      (column.title === '操作' && !('dataIndex' in column && column.dataIndex));
    const remark = column.key === 'remark' || column.title === '备注';
    const dataKey = 'dataIndex' in column ? column.dataIndex : column.key;
    const grow =
      !legacySizing &&
      typeof dataKey === 'string' &&
      growColumns.includes(dataKey) &&
      props.dataSource?.some((row) => {
        const value = (row as Record<string, unknown>)[dataKey];
        return typeof value === 'string'
          ? value.trim() !== ''
          : Array.isArray(value) && value.length > 0;
      });
    const limit = legacySizing
      ? column === fill
        ? 640
        : typeof width === 'number'
          ? width
          : 280
      : column === fill
        ? 600
        : typeof width === 'number'
          ? Math.min(width, 320)
          : 224;
    return {
      ...column,
      width: undefined,
      minWidth: undefined,
      className: [
        column.className,
        column === fill && 'content-sized-fill',
        grow && 'content-sized-grow',
        actions && 'content-sized-actions',
        remark && 'content-sized-remark',
      ]
        .filter(Boolean)
        .join(' '),
      onCell(record: Row, index?: number) {
        const cell = column.onCell?.(record, index);
        return {
          ...cell,
          style: {
            ...cell?.style,
            '--content-sized-default-limit': `${limit}px`,
          } as CSSProperties,
        };
      },
    };
  });
  const height = typeof scroll?.y === 'number' ? `${scroll.y}px` : scroll?.y;
  return (
    <Table<Row>
      {...props}
      columns={adjusted}
      className={[
        className,
        'content-sized-table',
        !legacySizing && 'content-sized-balanced',
        height && 'content-sized-scroll',
      ]
        .filter(Boolean)
        .join(' ')}
      components={{ body: { cell: ContentSizedCell } }}
      tableLayout="auto"
      scroll={{
        x: true,
        scrollToFirstRowOnChange: scroll?.scrollToFirstRowOnChange,
      }}
      style={
        height
          ? ({ ...style, '--content-sized-height': height } as CSSProperties)
          : style
      }
    />
  );
}
