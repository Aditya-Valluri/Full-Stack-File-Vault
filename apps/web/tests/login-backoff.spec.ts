import {test,expect} from './fixtures';

test('five failed passwords show a server restriction and keep email recovery available',async({page})=>{
 await page.goto('/');
 const email='unknown-backoff-'+Date.now()+'@example.test';
 await page.getByLabel('Username or email',{exact:true}).fill(email);
 await page.getByLabel('Password',{exact:true}).fill('an incorrect private password');
 for(let attempt=1;attempt<=5;attempt++){
  const response=page.waitForResponse(r=>r.url().endsWith('/graphql')&&r.request().postData()?.includes('mutation Login')===true);
  await page.getByRole('button',{name:'Sign in',exact:true}).click();
  await response;
  if(attempt<5)await expect(page.getByText('The username, email or password was not accepted.')).toBeVisible();
 }
 await expect(page.getByText('Too many unsuccessful sign-in attempts. Try again in 5 minutes or reset your password.')).toBeVisible();
 await page.getByRole('button',{name:'Reset password',exact:true}).click();
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('button',{name:'Send reset code',exact:true}).click();
 await expect(page.getByLabel('Reset code (7 digits)',{exact:true})).toBeVisible();
});
