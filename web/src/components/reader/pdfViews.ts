/**
 * How a PDF's pages are grouped into "views" — the unit the reader advances
 * by: one page, or one two-page spread.
 */

/** Groups pages into views. With two per view the cover stands alone and
 * the rest pair up (2-3, 4-5, …), so facing pages face each other the way
 * they do in the printed book. */
export function buildViews(numPages: number, perView: 1 | 2): number[][] {
  const views: number[][] = [];
  if (perView === 1) {
    for (let p = 1; p <= numPages; p++) views.push([p]);
    return views;
  }
  if (numPages >= 1) views.push([1]);
  for (let p = 2; p <= numPages; p += 2) {
    views.push(p + 1 <= numPages ? [p, p + 1] : [p]);
  }
  return views;
}

/** The index of the view showing `page`. */
export function viewIndexOf(page: number, perView: 1 | 2): number {
  return perView === 1 ? page - 1 : Math.floor(page / 2);
}
