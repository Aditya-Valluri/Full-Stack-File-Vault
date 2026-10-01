import {useEffect,useRef,useState} from 'react';
import {AdminUploadersDocument,type AdminUploadersQuery} from '../generated/graphql';
import {explainError,query} from '../lib/api';
import {Button,Notice} from './ui';

type User=AdminUploadersQuery['adminUsers']['nodes'][number];
type Props={value:string;selectedLabel:string;onChange:(id:string,label:string)=>void};

// Reuse the bounded admin-only user query. Explicit pagination avoids downloading
// the entire account directory just to open the file list.
export function UploaderSelector({value,selectedLabel,onChange}:Props){
 const [users,setUsers]=useState<User[]>([]);
 const [cursor,setCursor]=useState<string|null>();
 const [hasMore,setHasMore]=useState(false);
 const [busy,setBusy]=useState(true);
 const [error,setError]=useState('');
 const version=useRef(0);
 async function load(after?:string){
  const current=version.current;setBusy(true);setError('');
  try{
   const result=await query(AdminUploadersDocument,{first:50,after});
   if(current!==version.current)return;
   setUsers(previous=>[...new Map((after?[...previous,...result.adminUsers.nodes]:result.adminUsers.nodes).map(user=>[user.id,user])).values()]);
   setCursor(result.adminUsers.pageInfo.endCursor);setHasMore(result.adminUsers.pageInfo.hasNextPage);
  }catch(failure){if(current===version.current)setError(explainError(failure));}
  finally{if(current===version.current)setBusy(false);}
 }
 useEffect(()=>{version.current++;void load();return()=>{version.current++;};},[]);
 const label=(user:User)=>user.loginName ?? 'Account without a login identity ('+user.id+')';
 const options=[...users].sort((a,b)=>label(a).localeCompare(label(b))||a.id.localeCompare(b.id));
 return <div className="owner-filter">
  <label>Filter by uploader<select aria-label="Filter by uploader" value={value} onChange={event=>{
   const id=event.target.value;const user=users.find(item=>item.id===id);
   onChange(id,user?label(user):'');
  }}>
   <option value="">All uploaders</option>
   {value&&!users.some(user=>user.id===value)&&<option value={value}>{selectedLabel||'Selected uploader ('+value+')'}</option>}
   {options.map(user=><option key={user.id} value={user.id}>{label(user)}</option>)}
  </select></label>
  {busy&&<span role="status">Loading uploaders…</span>}
  {hasMore&&<Button variant="secondary" disabled={busy} onClick={()=>void load(cursor??undefined)}>Load more uploaders</Button>}
  {error&&<><Notice error>{error}</Notice><Button variant="secondary" disabled={busy} onClick={()=>void load(cursor??undefined)}>Retry uploaders</Button></>}
  <Button variant="ghost" disabled={!value} onClick={()=>onChange('','')}>Clear</Button>
 </div>;
}
