import React from 'react';
import * as jsx from 'react/jsx-runtime';
import * as jsxDev from 'react/jsx-dev-runtime';
import * as reactDOM from 'react-dom';
import * as reactDOMClient from 'react-dom/client';
import * as antd from 'antd';
import * as icons from '@ant-design/icons';

// 桥接模块只读取主应用实例，不再打包第二份 React 或组件库。
const registry = {
  react: React,
  'react/jsx-runtime': jsx,
  'react/jsx-dev-runtime': jsxDev,
  'react-dom': reactDOM,
  'react-dom/client': reactDOMClient,
  antd,
  '@ant-design/icons': icons,
};

declare global {
  var __PANEL_SHARED__: typeof registry;
}
globalThis.__PANEL_SHARED__ = registry;
export const sharedReady = Promise.resolve();
