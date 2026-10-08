// Check every built local href/src, including project-base paths and anchors.
import fs from 'node:fs';
import path from 'node:path';
const dir=path.resolve('dist');
const repo=process.env.GITHUB_REPOSITORY||'ucgeorge/switchboard';
const [owner,name]=repo.split('/');
const base=(process.env.SITE_BASE||(name===`${owner}.github.io`?'/':`/${name}`)).replace(/\/$/,'');
function files(d){return fs.readdirSync(d,{withFileTypes:true}).flatMap(e=>e.isDirectory()?files(path.join(d,e.name)):[path.join(d,e.name)])}
const html=files(dir).filter(f=>f.endsWith('.html'));let broken=[];let count=0;
for(const file of html){
 const text=fs.readFileSync(file,'utf8');
 for(const m of text.matchAll(/\b(?:href|src)="([^"]+)"/g)){
  let href=m[1].replaceAll('&amp;','&');if(/^(https?:|data:|mailto:|javascript:|tel:)/.test(href))continue;
  const sourcePath=base+'/'+path.relative(dir,file).replace(/index\.html$/,'');
  const url=new URL(href,'https://local.test'+sourcePath);
  let pathname=decodeURIComponent(url.pathname);
  if(base && pathname!==base && !pathname.startsWith(base+'/')){broken.push(`${file}: outside deployment base ${href}`);continue}
  pathname=pathname.slice(base.length);
  let target=path.join(dir,pathname);
  if(fs.existsSync(target)&&fs.statSync(target).isDirectory())target=path.join(target,'index.html');
  if(!fs.existsSync(target)){broken.push(`${path.relative(dir,file)}: missing ${href}`);continue}
  if(url.hash&&target.endsWith('.html')){
   const id=decodeURIComponent(url.hash.slice(1));const body=fs.readFileSync(target,'utf8');
   if(!body.includes(`id="${id}"`))broken.push(`${path.relative(dir,file)}: missing anchor ${href}`);
  }
  count++;
 }
}
if(broken.length){console.error([...new Set(broken)].join('\n'));process.exit(1)}
console.log(`Verified ${count} local asset/page/anchor links across ${html.length} HTML pages.`);
