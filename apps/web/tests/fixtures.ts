import {test as base,expect,type Page} from '@playwright/test';
import {execFile} from 'node:child_process';

export {expect,type Page};
// Each scenario starts with a fresh admission budget in the exact disposable
// container created by setup.ts. This never changes application policy and does
// not reset counters within a scenario (including the lockout scenario).
export const test=base.extend<{authBudget:void}>({
 authBudget:[async({},use)=>{
  const container=process.env.E2E_DATABASE_CONTAINER ?? '';
  if(!/^full-stack-file-vault-browser-[0-9a-f]{12}$/.test(container))throw new Error('Missing isolated browser database');
  await new Promise<void>((resolve,reject)=>{
   const child=execFile('docker',['exec','-i',container,'psql','-v','ON_ERROR_STOP=1','-U','postgres','-d','vault_browser'],{windowsHide:true,timeout:10000},error=>error?reject(new Error('Cannot reset isolated test admission budget')):resolve());
   child.stdin!.end("TRUNCATE vault.login_attempts; UPDATE vault.login_budget SET attempts=0,window_start=date_trunc('minute',clock_timestamp());");
  });
  await use();
 },{auto:true}],
});
