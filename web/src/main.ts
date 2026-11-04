// Browser entry (index.html): `depgraph serve` / `watch` over HTTP.
import { httpHost } from './host/http';
import { start } from './ui';

start(httpHost());
