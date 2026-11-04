// MCP App entry (`vite build --mode mcp-app` puts it in place of src/main.ts):
// `depgraph mcp`, rendered inside an MCP Apps host.
import { mcpHost } from './host/mcp';
import { start } from './ui';

start(mcpHost());
