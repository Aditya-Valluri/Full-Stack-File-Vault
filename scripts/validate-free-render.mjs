// Billing guard for the repository's temporary demo Blueprint, not a substitute
// for Render's server-side validation or account-level usage/billing controls.
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { parseDocument } from '../apps/web/node_modules/yaml/dist/index.js';

const root=resolve(import.meta.dirname,'..');
const document=parseDocument(await readFile(resolve(root,'render.yaml'),'utf8'),{uniqueKeys:true});
if(document.errors.length)throw new Error('Invalid or duplicate Blueprint YAML keys.');
const blueprint=document.toJS();
function keys(value,allowed) {
 if(!value || typeof value!=='object' || Array.isArray(value))throw new Error('Invalid Blueprint object.');
 for(const key of Object.keys(value))if(!allowed.includes(key))throw new Error('Unreviewed Blueprint field: '+key);
}
keys(blueprint,['services','databases']);
if(blueprint.services?.length!==1 || blueprint.databases?.length!==1)throw new Error('Demo requires exactly one free web service and database.');
const [service]=blueprint.services,[database]=blueprint.databases;
keys(service,['type','name','runtime','plan','region','dockerfilePath','dockerContext','dockerCommand','autoDeployTrigger','healthCheckPath','envVars']);
keys(database,['name','plan','region','databaseName','user','postgresMajorVersion','ipAllowList']);
if(service.type!=='web' || service.runtime!=='docker' || service.plan!=='free' || database.plan!=='free')
 throw new Error('Only Free web and PostgreSQL plans are allowed.');
if(service.region!==database.region || service.autoDeployTrigger!=='off')throw new Error('Demo region or deployment policy differs.');
if(!Array.isArray(database.ipAllowList) || database.ipAllowList.length!==0)throw new Error('Database must disable public IP access.');
const operator=service.envVars?.find(entry=>entry.key==='DEMO_OPERATOR_URL');
if(operator?.fromDatabase?.name!==database.name || operator.fromDatabase.property!=='connectionString')throw new Error('Wrong private database reference.');
for(const entry of service.envVars??[]) {
 keys(entry,['key','fromDatabase','generateValue']);
 if(entry!==operator && entry.generateValue!==true)throw new Error('Demo secrets must be provider-generated.');
}
console.log('PASS: Blueprint requests only free compute/database, with no paid disk, pre-deploy phase, or unreviewed resource fields.');
