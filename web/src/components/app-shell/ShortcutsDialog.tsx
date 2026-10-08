/**
 * The keyboard shortcuts, discoverable with `?` or from the help button.
 */

import { Dialog } from '../Dialog';

const GROUPS: Array<{ title: string; keys: Array<[string[], string]> }> = [
  {
    title: 'Anywhere',
    keys: [
      [['/'], 'Search'],
      [['?'], 'Show these shortcuts'],
      [['Esc'], 'Close a menu, dialog, or the viewer'],
    ],
  },
  {
    title: 'In a grid',
    keys: [
      [['←', '→', '↑', '↓'], 'Move between items'],
      [['Enter'], 'Open'],
      [['Space'], 'Select or deselect'],
      [['Shift', 'Click'], 'Select a range'],
      [['Ctrl', 'A'], 'Select all'],
      [['Delete'], 'Move the selection to the trash'],
    ],
  },
  {
    title: 'In the viewer',
    keys: [
      [['←', '→'], 'Previous and next'],
      [['+', '−'], 'Zoom in and out'],
      [['0'], 'Fit to window'],
      [['Space'], 'Play or pause a video'],
      [['F'], 'Fullscreen'],
      [['I'], 'Details'],
      [['S'], 'Slideshow'],
      [['H'], 'Hide controls'],
    ],
  },
];

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog
      open={open}
      title="Keyboard shortcuts"
      onClose={onClose}
      size="medium"
      testId="shortcuts"
    >
      <div className="shortcuts">
        {GROUPS.map((group) => (
          <section key={group.title} className="shortcuts-group">
            <h3>{group.title}</h3>
            <dl>
              {group.keys.map(([keys, description]) => (
                <div key={description} className="shortcuts-row">
                  <dt>
                    {keys.map((key) => (
                      <kbd key={key}>{key}</kbd>
                    ))}
                  </dt>
                  <dd>{description}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
