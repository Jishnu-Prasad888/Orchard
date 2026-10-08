/**
 * Inserts `ref` at the textarea's current caret position, then restores the
 * caret after the inserted text and fires an input event so React picks up the
 * change.
 */
export function insertRefAtCursor(textarea: HTMLTextAreaElement, ref: string): void {
  const start = textarea.selectionStart;
  const end = textarea.selectionEnd;
  const before = textarea.value.slice(0, start);
  const after = textarea.value.slice(end);
  textarea.value = `${before}${ref}${after}`;
  const caret = start + ref.length;
  textarea.setSelectionRange(caret, caret);
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}
