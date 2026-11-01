export function paginationState(total: number, offset: number, limit: number) {
  if (
    ![total, offset, limit].every(Number.isSafeInteger) ||
    total < 0 ||
    offset < 0 ||
    limit < 1
  ) {
    throw new RangeError('Invalid pagination bounds');
  }
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const current = Math.floor(offset / limit) + 1;
  const anchor = Math.min(current, totalPages);
  const pages = new Set([1, totalPages, anchor - 1, anchor, anchor + 1]);
  if (totalPages <= 7) {
    for (let page = 1; page <= totalPages; page++) pages.add(page);
  } else {
    if (anchor <= 3) for (const page of [2, 3, 4]) pages.add(page);
    if (anchor >= totalPages - 2)
      for (const page of [totalPages - 3, totalPages - 2, totalPages - 1])
        pages.add(page);
  }
  const numbers = [...pages]
    .filter((page) => page >= 1 && page <= totalPages)
    .sort((a, b) => a - b);
  const items: (number | 'ellipsis')[] = [];
  let previousPage = 0;
  for (const page of numbers) {
    if (previousPage && page - previousPage === 2) items.push(previousPage + 1);
    else if (previousPage && page - previousPage > 2) items.push('ellipsis');
    items.push(page);
    previousPage = page;
  }
  return {
    current,
    totalPages,
    items,
    previous: Math.max(1, Math.min(totalPages, current - 1)),
    next: Math.min(totalPages, current + 1),
  };
}
