// Every TypeScript import / export form and declaration kind.
import { query } from '../db';
import type { Request } from 'express';
import Default, { named as alias } from "./shared";
import * as util from '@/db/util';
import './side-effect';
import config from '../../config.json';
import { sub } from '@scope/pkg/sub';
export * from './shared';
export * as ns from './shared';
export { format } from '@/db/util';
export { query as q };

export class UserController {
  list() {
    return query("u");
  }
}
export abstract class Base {}
export interface Row {
  id: number;
}
export type ID = string | number;
export enum Color {
  Red,
  Green,
}
export const enum Flag {
  A,
}
export function handler(req: Request) {
  function nested() {}
  return nested;
}
export function* gen() {
  yield 1;
}
export async function load() {}
export const Helper = () => 1,
  other = 2;
export let fexpr = function () {};
export default function main() {}
declare module "foo" {}
namespace Internal {
  export const x = 1;
}
function internal() {}
const local = 1;
let late: number;
var legacy = function* () {};
interface Hidden {}
type Private = {};
const 日本語 = "x"; export function afterUnicode() {}
