export * from './app.js';
export { C } from './component';
import x from './missing';
import lodash from 'lodash';

export const value = x + lodash.VERSION;
