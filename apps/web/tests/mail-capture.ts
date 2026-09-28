import { createServer, type Server } from 'node:net';
import { writeFile } from 'node:fs/promises';

/** Loopback-only SMTP sink for synthetic browser-test verification messages. */
export async function captureMail(path: string): Promise<{ address: string; close: () => Promise<void> }> {
 const sockets = new Set<import('node:net').Socket>();
 const server: Server = createServer(socket => {
  sockets.add(socket); socket.on('close', () => sockets.delete(socket));
  socket.setTimeout(10_000, () => socket.destroy());
  socket.on('error', () => socket.destroy());
  socket.write('220 localhost test mail\r\n');
  let pending = ''; let data = false; let message = '';
  socket.on('data', chunk => {
   pending += chunk.toString();
   if (pending.length + message.length > 16_384) { socket.destroy(); return; }
   let index: number;
   while ((index = pending.indexOf('\r\n')) !== -1) {
    const line = pending.slice(0, index); pending = pending.slice(index + 2);
    if (data) {
     if (line === '.') {
      data = false;
      const captured = message; message = '';
      void writeFile(path, captured, { mode: 0o600 }).then(() => socket.write('250 captured\r\n'), () => socket.destroy());
     } else { message += line + '\r\n'; }
    } else if (/^(EHLO|HELO|MAIL FROM:|RCPT TO:)/i.test(line)) { socket.write('250 OK\r\n'); }
    else if (line === 'DATA') { data = true; socket.write('354 End with dot\r\n'); }
    else if (line === 'QUIT') { socket.end('221 Bye\r\n'); }
    else { socket.write('500 Unsupported\r\n'); }
   }
  });
 });
 await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
 const address = server.address();
 if (!address || typeof address === 'string') throw new Error('SMTP fixture unavailable.');
 return { address: '127.0.0.1:' + address.port, close: async () => {
  for (const socket of sockets) socket.destroy();
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
 } };
}
