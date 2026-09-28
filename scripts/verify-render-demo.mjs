// Local rehearsal of the free Render demo with disposable local staging. Creates only
// uniquely named disposable Docker resources; never targets the existing app.
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { createServer } from 'node:https';
import { request as httpRequest } from 'node:http';
import { chromium } from '../apps/web/node_modules/playwright/index.mjs';

const root = resolve(import.meta.dirname, '..');
const name = 'full-stack-file-vault-render-check-' + randomBytes(6).toString('hex');
const network = name + '-net', database = name + '-db';
const origin = 'https://localhost:18443';
const dbPassword = randomBytes(32).toString('hex');
const secrets = {
 POSTGRES_PASSWORD: randomBytes(32).toString('hex'),
 DEMO_OPERATOR_URL: 'postgresql://vault_operator:' + dbPassword + '@' + database + '/vault_demo?sslmode=disable',
 DEMO_RUNTIME_SEED: randomBytes(32).toString('base64'),
 DEMO_GC_SEED: randomBytes(32).toString('base64'),
 DEMO_REVIEWER_PASSWORD: randomBytes(24).toString('hex'),
 DEMO_ADMIN_PASSWORD: randomBytes(24).toString('hex'),
 PUBLIC_ORIGIN: origin,
};
async function docker(args, input = '') {
 const child = spawn('docker', args, { cwd: root, windowsHide: true, env: { ...process.env, ...secrets }, stdio: ['pipe','pipe','pipe'] });
 let output = '', diagnostic = ''; child.stdout.on('data', chunk => { output += chunk; });
 child.stderr.on('data', chunk => { if (diagnostic.length < 2000000) diagnostic += chunk; });
 function failure() {
  if (diagnostic.length >= 2000000) return new Error('Docker diagnostic exceeded the safe output limit.');
  let safe = diagnostic;
  for (const secret of [dbPassword, ...Object.values(secrets)]) safe = safe.split(secret).join('[redacted]');
  return new Error('Docker rehearsal step failed: ' + args[0] + ': ' + safe.slice(0, 1500));
 }
 const exit = new Promise((done, fail) => {
  child.on('error', fail); child.on('close', code => code === 0 ? done() : fail(failure()));
 });
 child.stdin.on('error', () => undefined); child.stdin.end(input);
 await exit; return output.trim();
}
const envArgs = Object.keys(secrets).filter(key => key !== 'POSTGRES_PASSWORD').flatMap(key => ['-e', key]);
const sql = text => docker(['exec','-i',database,'psql','-At','-v','ON_ERROR_STOP=1','-U','postgres','-d','vault_demo'], text);
const setup = () => docker(['run','--rm','--network',network,...envArgs,'full-stack-file-vault-render:local','/app/render-demo','setup']);
let netCreated = false, dbCreated = false, appCreated = false, browser, edge;
async function startApp() {
 await docker(['run','-d','--name',name,'--network',network,'--memory','512m','--memory-swap','512m','--read-only','--tmpfs','/tmp:rw,noexec,nosuid,size=16m','--tmpfs','/data:rw,noexec,nosuid,size=64m,uid=65532,gid=65532,mode=0700','--cap-drop','ALL','--security-opt','no-new-privileges:true','-p','127.0.0.1::10000',...envArgs,'full-stack-file-vault-render:local']);
 appCreated=true;
 return (await docker(['port',name,'10000/tcp'])).split(':').at(-1);
}
async function ready(port) {
 for (let attempt=0;attempt<40;attempt++) {
  try { const r=await fetch('http://127.0.0.1:' + port + '/readyz', { signal: AbortSignal.timeout(3000) }); if(r.ok)return; } catch {}
  await new Promise(done=>setTimeout(done,1000));
 }
 throw new Error('Demo readiness failed.');
}
async function login(page, username, password) {
 await page.goto(origin);
 await page.getByLabel('Username or email', {exact:true}).fill(username);
 await page.getByLabel('Password', {exact:true}).fill(password);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();
 await page.getByRole('heading',{name:'My files',exact:true}).waitFor();
 if(await page.locator('.account-name').textContent()!==username)throw new Error('Wrong demo identity.');
}
try {
 const [cert,key]=await Promise.all([readFile(resolve(root,'.secrets/application/tls.crt')),readFile(resolve(root,'.secrets/application/tls.key'))]);
 await docker(['network','create',network]);netCreated=true;
 await docker(['run','-d','--name',database,'--network',network,'-e','POSTGRES_PASSWORD','-e','POSTGRES_DB=vault_demo','postgres:17-bookworm']);dbCreated=true;
 for(let i=0;i<30;i++){
  try{await docker(['exec',database,'pg_isready','-h','127.0.0.1','-U','postgres','-d','vault_demo']);break;}
  catch{if(i===29)throw new Error('Database startup failed.');await new Promise(done=>setTimeout(done,1000));}
 }
 await sql("CREATE ROLE vault_operator LOGIN CREATEROLE NOSUPERUSER PASSWORD '"+dbPassword+"'; ALTER DATABASE vault_demo OWNER TO vault_operator;");
 console.log('[render rehearsal] Applying migrations as a non-superuser database owner');
 let port=await startApp();
 await ready(port);
 edge=createServer({cert,key},(incoming,outgoing)=>{
  const upstream=httpRequest({hostname:'127.0.0.1',port,path:incoming.url,method:incoming.method,headers:incoming.headers},response=>{
   outgoing.writeHead(response.statusCode,response.headers);response.pipe(outgoing);
  });
  upstream.on('error',()=>{outgoing.writeHead(502);outgoing.end();});
  incoming.on('aborted',()=>upstream.destroy());incoming.pipe(upstream);
 });
 await new Promise((done,fail)=>{edge.once('error',fail);edge.listen(18443,'127.0.0.1',done);});
 browser=await chromium.launch();
 const context=await browser.newContext({ignoreHTTPSErrors:true});
 const page=await context.newPage();
 await login(page,'reviewer',secrets.DEMO_REVIEWER_PASSWORD);
 const cookies=await context.cookies();
 if(!cookies.some(c=>c.name==='__Host-vault_session'&&c.secure&&c.httpOnly))throw new Error('Demo cookie is not secure.');
 const bytes=Buffer.from('Render rehearsal content '+randomBytes(8).toString('hex'));
 await page.getByRole('button',{name:'Upload files',exact:true}).click();
 // The demo UI must reject the first byte beyond the backend's file budget,
 // before submitting a request; successful small uploads must still work afterward.
 await page.getByText('Choose up to 10 files. Each batch can contain up to 10 MB, within your remaining quota.',{exact:true}).waitFor();
 await page.getByLabel('Select files to upload').setInputFiles({name:'over-limit.bin',mimeType:'application/octet-stream',buffer:Buffer.alloc(10_000_001)});
 await page.getByRole('dialog').getByText('Choose up to 10 files, each no larger than 10 MB.',{exact:true}).waitFor();
 if(await page.getByRole('button',{name:'Upload selected files',exact:true}).isEnabled())throw new Error('Oversized demo selection remained uploadable.');
 await page.getByLabel('Select files to upload').setInputFiles([
  {name:'review-notes.txt',mimeType:'text/plain',buffer:bytes},
  {name:'review-copy.txt',mimeType:'text/plain',buffer:bytes},
 ]);
 await page.getByRole('button',{name:'Upload selected files'}).click();
 await page.getByText('Uploaded 2 files.',{exact:true}).waitFor();
 await page.getByText('50.00% less unique content',{exact:false}).waitFor();
 await page.getByRole('button',{name:'Share review-notes.txt',exact:true}).click();
 await page.getByRole('button',{name:'Create sharing link',exact:true}).click();
 const link=await page.getByLabel('Your new link',{exact:true}).inputValue();
 const guestContext=await browser.newContext({ignoreHTTPSErrors:true}),guest=await guestContext.newPage();
 await guest.goto(link);
 await guest.getByRole('heading',{name:'review-notes.txt',exact:true}).waitFor();
 let event=guest.waitForEvent('download');
 await guest.getByRole('button',{name:'Download file',exact:true}).click();
 if(!(await readFile(await (await event).path())).equals(bytes))throw new Error('Shared bytes differ.');
 await guestContext.close();
 console.log('[render rehearsal] Replacing the app container and discarding all local files');
 await setup();
 await docker(['exec',name,'sh','-c','touch /data/blobs/ephemeral-marker']);
 await docker(['rm','-f',name]);appCreated=false;
 port=await startApp();
 await docker(['exec',name,'sh','-c','test ! -e /data/blobs/ephemeral-marker']);
 await ready(port);
 await page.goto(origin);
 await page.getByRole('button',{name:'Download review-notes.txt',exact:true}).waitFor();
 event=page.waitForEvent('download');
 await page.getByRole('button',{name:'Download review-notes.txt',exact:true}).click();
 if(!(await readFile(await (await event).path())).equals(bytes))throw new Error('Restart lost stored bytes.');
 const adminContext=await browser.newContext({ignoreHTTPSErrors:true}),admin=await adminContext.newPage();
 await login(admin,'reviewer-admin',secrets.DEMO_ADMIN_PASSWORD);
 await admin.getByRole('button',{name:'Administration',exact:true}).click();
 await admin.getByRole('heading',{name:'Administration',exact:true}).waitFor();
 await admin.getByRole('tab',{name:'All files',exact:true}).click();
 await admin.getByText('review-notes.txt',{exact:true}).waitFor();
 const aggregate=await sql("SELECT (SELECT count(*) FROM vault.users)||','||(SELECT count(*) FROM vault.files)||','||(SELECT count(*) FROM vault.blobs);");
 if(aggregate!=='2,2,1')throw new Error('Demo setup duplicated accounts or content.');
 const stored=await sql("SELECT count(*)||','||COALESCE(sum(octet_length(content)),0) FROM vault_demo.objects;");
 if(stored!=='1,'+bytes.length)throw new Error('Bytes are not deduplicated in PostgreSQL.');
 // A second clean client can still use the original share after replacement.
 const restoredGuestContext=await browser.newContext({ignoreHTTPSErrors:true});
 const restoredGuest=await restoredGuestContext.newPage();
 await restoredGuest.goto(link);
 event=restoredGuest.waitForEvent('download');
 await restoredGuest.getByRole('button',{name:'Download file',exact:true}).click();
 if(!(await readFile(await (await event).path())).equals(bytes))throw new Error('Replacement broke shared bytes.');
 await restoredGuestContext.close();
 // Deleting one logical copy retains the shared bytes; the last copy is collected.
 for(const filename of ['review-notes.txt','review-copy.txt']) {
  await page.getByRole('button',{name:'Delete '+filename,exact:true}).click();
  await page.getByRole('button',{name:'Delete file',exact:true}).click();
  await page.getByRole('button',{name:'Delete '+filename,exact:true}).waitFor({state:'detached'});
  if(filename==='review-notes.txt' && await sql('SELECT count(*) FROM vault_demo.objects;')!=='1')
   throw new Error('Deleting a duplicate removed shared bytes.');
 }
 // Advance only this disposable fixture's grace period, then let the real worker run.
 await sql("UPDATE vault.blobs SET gc_after=clock_timestamp()-interval '1 second' WHERE state='GC_PENDING';");
 let cleaned=false;
 for(let i=0;i<25;i++) {
  const state=await sql("SELECT used_bytes||','||object_count FROM vault_demo.capacity;");
  if(state==='0,0') {cleaned=true;break;}
  await new Promise(done=>setTimeout(done,2000));
 }
 if(!cleaned)throw new Error('Collector did not release demo byte capacity.');
 if(await sql("SELECT used_bytes FROM vault.users u JOIN vault.credentials c ON c.user_id=u.id WHERE c.login_name='reviewer';")!=='0')
  throw new Error('Deletion did not release logical quota.');
 if((await fetch('http://127.0.0.1:'+port+'/metrics')).status!==404)throw new Error('Private metrics exposed.');
 await mkdir(resolve(root,'tmp'),{recursive:true});
 for(const width of [1024,390]) {
  await page.setViewportSize({width,height:800});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth))
   throw new Error('Application layout overflows the viewport.');
 }
 await page.setViewportSize({width:1280,height:800});
 await page.screenshot({path:resolve(root,'tmp/render-demo-rehearsal.png'),fullPage:true});
 await writeFile(resolve(root,'tmp/render-demo-verification.json'),JSON.stringify({testedAt:new Date().toISOString(),nonSuperuserMigrations:true,repeatSetup:true,secureCookie:true,persistentReplacement:true,sharedDownload:true,adminFiles:true,uploadLimit:true,duplicateDeletion:true,byteCapacityReleased:true},null,2));
 console.log('PASS: Free Render demo, separate roles, TLS-edge login, PostgreSQL bytes, deduplication, sharing, admin, ephemeral container replacement and deletion/GC.');
} finally {
 if(browser)await browser.close();
 if(edge)await new Promise(done=>edge.close(done));
 if(appCreated)await docker(['rm','-f',name]).catch(()=>console.error('Retained rehearsal app: '+name));
 if(dbCreated)await docker(['rm','-f','-v',database]).catch(()=>console.error('Retained rehearsal database: '+database));
 if(netCreated)await docker(['network','rm',network]).catch(()=>console.error('Retained rehearsal network: '+network));
}
