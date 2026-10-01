import {test,expect,type Page} from './fixtures';
import {readFile} from 'node:fs/promises';

async function signInAdmin(page:Page){
 await page.goto('/');
 await page.getByLabel('Username or email',{exact:true}).fill('e2e.admin');
 await page.getByLabel('Password',{exact:true}).fill(process.env.E2E_PASSWORD!);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();
 await page.getByRole('button',{name:'Administration',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Administration',exact:true})).toBeVisible();
}

test('admin resolves verified email users and file uploaders without changing content permissions',async({page,browser})=>{
 const email='admin-display-'+Date.now()+'@example.test';
 await page.goto('/');
 await page.getByRole('button',{name:'New user? Create account',exact:true}).click();
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('button',{name:'Send verification code',exact:true}).click();
 await page.getByRole('button',{name:'I received a registration code',exact:true}).click();
 const mail=await readFile(process.env.E2E_MAIL_PATH!,'utf8');
 await page.getByLabel('Verification code (7 digits)',{exact:true}).fill(mail.match(/\b[0-9]{7}\b/)![0]);
 await page.getByLabel('New password',{exact:true}).fill('orbit meadow velvet lantern');
 await page.getByLabel('Confirm password',{exact:true}).fill('orbit meadow velvet lantern');
 await page.getByRole('button',{name:'Verify and create account',exact:true}).click();
 await expect(page.getByRole('heading',{name:'My files',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Upload files',exact:true}).click();
 await page.getByLabel('Select files to upload').setInputFiles({name:'email-owner.txt',mimeType:'text/plain',buffer:Buffer.from('Private email-owner content')});
 await page.getByRole('button',{name:'Upload selected files',exact:true}).click();
 await expect(page.getByText('Uploaded 1 file.',{exact:true})).toBeVisible();
 const context=await browser.newContext();const admin=await context.newPage();
 try{
  await admin.setViewportSize({width:390,height:844});
  await signInAdmin(admin);
  expect(await admin.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  const user=admin.getByRole('row').filter({hasText:email});
  await expect(user).toHaveCount(1);
  await expect(admin.getByRole('row').filter({hasText:'e2e.admin'})).toHaveCount(1);
  await user.getByRole('button',{name:'Files',exact:true}).click();
  const selector=admin.getByLabel('Filter by uploader',{exact:true});
  await expect(selector.locator('option:checked')).toHaveText(email);
  await expect(admin.getByRole('row').filter({hasText:'email-owner.txt'})).toContainText(email);
  await expect(admin.getByRole('button',{name:'Download email-owner.txt',exact:true})).toHaveCount(0);
  await selector.selectOption({label:'e2e.admin'});
  await expect(admin.getByRole('row').filter({hasText:'email-owner.txt'})).toHaveCount(0);
  await selector.selectOption({label:email});
  await expect(admin.getByRole('row').filter({hasText:'email-owner.txt'})).toBeVisible();
  await admin.getByRole('button',{name:'Clear',exact:true}).click();
  await expect(selector).toHaveValue('');
 }finally{await context.close();}
});

test('uploader selector paginates explicitly and submits UUID filters',async({page})=>{
 await signInAdmin(page);
 const id=(n:number)=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0');
 const user=(n:number)=>({id:id(n),loginName:n===51?'last-uploader@example.test':'uploader-'+n,role:'USER',usedBytes:'0',quotaBytes:'10000000',disabledAt:null,createdAt:'2026-01-01T00:00:00Z'});
 let pages=0;
 await page.route('**/graphql',async route=>{
  const body=route.request().postDataJSON();
  if(body.operationName==='AdminUploaders'&&body.variables.first===50){
   pages++;
   const next=body.variables.after===id(50);
   await route.fulfill({contentType:'application/json',body:JSON.stringify({data:{adminUsers:{nodes:next?[user(51)]:Array.from({length:50},(_,i)=>user(i+1)),pageInfo:{endCursor:next?id(51):id(50),hasNextPage:!next}}}})});
  }else await route.continue();
 });
 await page.getByRole('tab',{name:'All files',exact:true}).click();
 await expect(page.getByLabel('Filter by uploader').locator('option')).toHaveCount(51);
 expect(pages).toBe(1);
 await page.getByRole('button',{name:'Load more uploaders',exact:true}).click();
 await expect(page.getByLabel('Filter by uploader').locator('option')).toHaveCount(52);
 expect(pages).toBe(2);
 const request=page.waitForRequest(req=>req.url().endsWith('/graphql')&&req.postDataJSON().operationName==='AdminFiles'&&req.postDataJSON().variables.owner===id(51));
 await page.getByLabel('Filter by uploader').selectOption({label:'last-uploader@example.test'});
 await request;
 await expect(page.getByRole('button',{name:'Load more uploaders',exact:true})).toHaveCount(0);
});
