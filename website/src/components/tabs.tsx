import { Children, isValidElement, useId, useState } from 'react';

import type { ReactNode } from 'react';

// Every panel stays in the HTML (inactive ones `hidden`), so the prerendered page and its search index hold all tabs.
export function Tabs({ items, children }: { items: string[]; children: ReactNode }) {
  const id = useId();
  const [selected, setSelected] = useState(0);
  const panels = Children.toArray(children).filter(isValidElement);

  return (
    <div className='my-4'>
      <div role='tablist' className='border-border flex gap-1 overflow-x-auto border-b'>
        {items.map((item, index) => (
          <button
            key={item}
            type='button'
            role='tab'
            id={`${id}-tab-${index}`}
            aria-selected={index === selected}
            aria-controls={`${id}-panel-${index}`}
            onClick={() => setSelected(index)}
            className='text-muted hover:text-fg-strong aria-selected:border-accent aria-selected:text-accent -mb-px cursor-pointer border-b-2 border-transparent px-3 py-1.5 text-[0.9rem] font-medium whitespace-nowrap'
          >
            {item}
          </button>
        ))}
      </div>
      {panels.map((panel, index) => (
        <div key={panel.key} role='tabpanel' id={`${id}-panel-${index}`} aria-labelledby={`${id}-tab-${index}`} hidden={index !== selected}>
          {panel}
        </div>
      ))}
    </div>
  );
}

function TabsTab({ children }: { children: ReactNode }) {
  return children;
}

Tabs.Tab = TabsTab;
