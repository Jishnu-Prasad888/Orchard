/**
 * A row of the collection colours as filled circles: "No colour" first, then
 * the palette. Arrow keys move between them, as in any radio group.
 */

import { Ban, Check } from 'lucide-react';
import { useRef } from 'react';

import { COLLECTION_COLORS } from './colors';
import './CollectionColors.css';

export function ColorSwatches({
  value,
  onPick,
  label = 'Colour',
}: {
  /** The colour picked now; '' for none. */
  value: string;
  onPick: (color: string) => void;
  label?: string;
}) {
  const options = [{ id: '', label: 'No colour' }, ...COLLECTION_COLORS];
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const current = Math.max(
    0,
    options.findIndex((o) => o.id === value),
  );

  const onKeyDown = (event: React.KeyboardEvent, index: number) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
    if (!step) return;
    event.preventDefault();
    // Keep a surrounding menu from taking the arrows for its own items.
    event.stopPropagation();
    const next = (index + step + options.length) % options.length;
    refs.current[next]?.focus();
  };

  return (
    <div className="color-swatches" role="radiogroup" aria-label={label}>
      {options.map((option, i) => {
        const selected = option.id === value;
        return (
          <button
            key={option.id || 'none'}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="radio"
            aria-checked={selected}
            aria-label={option.label}
            title={option.label}
            tabIndex={i === current ? 0 : -1}
            className={option.id ? 'color-swatch' : 'color-swatch is-none'}
            data-collection-color={option.id || undefined}
            onClick={() => onPick(option.id)}
            onKeyDown={(event) => onKeyDown(event, i)}
            data-testid={`color-swatch-${option.id || 'none'}`}
          >
            {selected ? (
              <Check size={14} strokeWidth={3} aria-hidden="true" />
            ) : (
              !option.id && <Ban size={14} aria-hidden="true" />
            )}
          </button>
        );
      })}
    </div>
  );
}
