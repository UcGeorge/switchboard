import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
const repo = process.env.GITHUB_REPOSITORY || 'ucgeorge/switchboard';
const [owner, name] = repo.split('/');
const base = process.env.SITE_BASE || (name === `${owner}.github.io` ? '/' : `/${name}`);
export default defineConfig({
 site: process.env.SITE_URL || `https://${owner}.github.io`, base,
 integrations: [starlight({
  disable404Route: true,
  title: 'Switchboard', description: 'An OpenAI-compatible API served by MCP-connected agents.',
  social: [{ icon: 'github', label: 'GitHub', href: `https://github.com/${repo}` }],
  customCss: ['./src/styles/docs.css'],
  sidebar: [
   { label: 'Start here', items: ['docs/introduction','docs/installation','docs/updates','docs/quickstart'] },
   { label: 'Understand the system', items: ['docs/concepts','docs/lifecycle','docs/conversations'] },
   { label: 'Connect applications & agents', items: ['docs/openai','docs/mcp','docs/clients','docs/oauth','docs/human-in-the-loop'] },
   { label: 'Operate Switchboard', items: ['docs/dashboard','docs/cli','docs/remote','docs/settings','docs/deployment','docs/compose','docs/postgres','docs/hosting','docs/keel','docs/github-pages','docs/security','docs/observability','docs/backups','docs/troubleshooting'] },
   { label: 'Reference & project', items: ['docs/api-reference','docs/mcp-reference','docs/architecture','docs/contributing','docs/releases'] },
  ],
 })],
});
