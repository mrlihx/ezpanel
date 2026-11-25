export const isMac = /Mac|iPhone|iPad|iPod/.test(navigator.platform);
export const modifierKey = isMac ? '⌘' : 'Ctrl';
