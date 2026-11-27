import { Checkbox } from 'antd';
import type { Key } from 'react';

export default function DatabaseSelectAll({
  pageKeys,
  selected,
  onChange,
  disabled,
}: {
  pageKeys: Key[];
  selected: Key[];
  onChange: (keys: Key[]) => void;
  disabled?: boolean;
}) {
  const page = new Set(pageKeys);
  const count = selected.filter((key) => page.has(key)).length;
  return (
    <Checkbox
      checked={page.size > 0 && count === page.size}
      indeterminate={count > 0 && count < page.size}
      disabled={disabled || page.size === 0}
      onChange={({ target }) =>
        onChange(
          target.checked
            ? [...new Set([...selected, ...pageKeys])]
            : selected.filter((key) => !page.has(key)),
        )
      }
    >
      全选
    </Checkbox>
  );
}
