/**
 * Put text on the clipboard. The async Clipboard API only exists on secure
 * pages (HTTPS or localhost), and Orchard is often reached over plain HTTP on
 * the local network, so fall back to the older select-and-copy route there.
 * Resolves true when the text was copied, false when neither route worked.
 */
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Denied or unfocused; the fallback may still work.
    }
  }
  return copyWithSelection(text);
}

function copyWithSelection(text: string): boolean {
  const previousFocus = document.activeElement as HTMLElement | null;
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.top = '0';
  area.style.left = '0';
  area.style.opacity = '0';
  document.body.appendChild(area);
  area.select();
  let copied: boolean;
  try {
    copied = document.execCommand('copy');
  } catch {
    copied = false;
  }
  area.remove();
  previousFocus?.focus?.({ preventScroll: true });
  return copied;
}
