import { server } from './build/index.js';

// Authenticated credential uploads may take arbitrarily long. Keep the adapter's
// header deadline and graceful shutdown, but remove Node's whole-request deadline.
server.server.requestTimeout = 0;

export const httpServer = server.server;
