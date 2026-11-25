export const number = (value: number | null | undefined, suffix = '') =>
  value == null ? '--' : `${value.toFixed(1)}${suffix}`;

export const bytes = (value: number | null | undefined) => {
  if (value == null) return '--';
  const unit =
    value >= 1024 ** 4
      ? 4
      : value >= 1024 ** 3
        ? 3
        : value >= 1024 ** 2
          ? 2
          : value >= 1024
            ? 1
            : 0;
  return `${(value / 1024 ** unit).toFixed(1)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`;
};
