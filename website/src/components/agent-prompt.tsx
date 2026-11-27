import { agentPrompt, agentsMdSection } from '@/lib/agent-guide';

import { CopyButton } from './copy-button';

const blocks = {
  prompt: { title: 'Prompt for a coding agent', hint: 'Paste it into Claude Code, Codex, Cursor or any agent before asking it to run or share an app.', text: agentPrompt, label: 'Copy prompt' },
  'agents-md': { title: 'Section for AGENTS.md', hint: "Keep it in the repo so every agent session starts knowing the project's URLs.", text: agentsMdSection, label: 'Copy section' },
};

// A ready-to-paste text for agents, shown in full (so it is readable and searchable) with one button to copy it.
export function AgentPrompt({ kind = 'prompt' }: { kind?: keyof typeof blocks }) {
  const block = blocks[kind];
  return (
    <section className='border-border-strong bg-paper-2 my-6 overflow-hidden rounded-xl border'>
      <header className='border-border flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3'>
        <div>
          <p className='text-fg-strong text-[0.95rem] font-semibold'>{block.title}</p>
          <p className='text-muted text-[0.84rem]'>{block.hint}</p>
        </div>
        <CopyButton text={block.text} label={block.label} />
      </header>
      <pre className='text-fg max-h-[22rem] overflow-auto px-4 py-3.5 font-mono text-[0.8rem] leading-relaxed whitespace-pre-wrap'>{block.text}</pre>
    </section>
  );
}
