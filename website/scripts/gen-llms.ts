// Writes /llms.txt (what tsp is, and a line per page) and /llms-full.txt (every page as Markdown) into public/, from
// the same page list and agent texts the site renders. `bun run build` runs it first; `bun scripts/gen-llms.ts` alone.
import { agentPrompt, agentsMdSection, llmsIntro } from '../src/lib/agent-guide';
import { pageGroups, pages, siteName, siteUrl } from '../src/lib/pages';

const root = `${import.meta.dir}/..`;

// MDX to plain Markdown: an <Address> becomes its URL and a list of what each part means, the agent texts are spelled
// out, and the layout-only JSX (callouts, tabs, figures, download buttons) leaves its text behind.
function toMarkdown(mdx: string): string {
  return mdx
    .replace(/<Address[\s\S]*?\/>/g, (block) => {
      const parts = [...block.matchAll(/text: "([^"]*)", note: "([^"]*)"/g)];
      const url = parts.map(([, text]) => text).join('');
      return [`\`${url}\``, '', ...parts.map(([, text, note]) => `- \`${text}\`: ${note}`)].join('\n');
    })
    .replace(/<AgentPrompt kind="agents-md" \/>/g, `\`\`\`markdown\n${agentsMdSection}\n\`\`\``)
    .replace(/<AgentPrompt( kind="prompt")? \/>/g, `\`\`\`text\n${agentPrompt}\n\`\`\``)
    .replace(/<ReachMap \/>\n?/g, '')
    .replace(/<svg[\s\S]*?<\/svg>/g, '')
    .replace(/^\s*<\/?(Callout|Tabs|Tabs\.Tab|div|span|a|img|p)\b[^>]*>\s*$/gm, '')
    .replace(/<\/?(Callout|Tabs\.Tab|Tabs|div|span|b|a|img|p)\b[^>]*>/g, '')
    .replace(/^ {4}/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

const index = [
  `# ${siteName}`,
  '',
  `> ${llmsIntro}`,
  '',
  `Install: \`brew install meabed/tap/tsp\` or \`npm i -g tailscale-proxy\`. Every page in one file: ${siteUrl}/llms-full.txt`,
  '',
  ...pageGroups.flatMap((group) => [`## ${group.title}`, '', ...group.pages.map((page) => `- [${page.title}](${siteUrl}${page.path === '/' ? '' : page.path}): ${page.summary}`), '']),
  '## Agent prompt',
  '',
  '```text',
  agentPrompt,
  '```',
  '',
].join('\n');

const docs = await Promise.all(pages.filter((page) => page.path !== '/').map(async (page) => `<!-- ${siteUrl}${page.path} -->\n\n${toMarkdown(await Bun.file(`${root}/content${page.path}.mdx`).text())}`));
const full = [`# ${siteName}`, '', `> ${llmsIntro}`, '', '## Agent prompt', '', '```text', agentPrompt, '```', '', ...docs.map((doc) => `${doc}\n`)].join('\n');

await Bun.write(`${root}/public/llms.txt`, index);
await Bun.write(`${root}/public/llms-full.txt`, full);
process.stdout.write(`llms.txt: ${pages.length} pages; llms-full.txt: ${Math.round(full.length / 1024)} KB\n`);
