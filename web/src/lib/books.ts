/**
 * What counts as a book. Orchard indexes, lists and accepts only these
 * (the server enforces the same list, `media.BookExtensions`); anything
 * else in a library's folders stays on disk but never appears.
 */

export const BOOK_EXTENSIONS = ['epub', 'pdf'] as const;

/** For a file input's `accept`: the extensions and their MIME types. */
export const BOOK_ACCEPT = '.epub,.pdf,application/epub+zip,application/pdf';

/** Whether a file name is a book, by extension, whatever its case. */
export function isBookName(name: string): boolean {
  const dot = name.lastIndexOf('.');
  if (dot < 0) return false;
  return (BOOK_EXTENSIONS as readonly string[]).includes(name.slice(dot + 1).toLowerCase());
}
