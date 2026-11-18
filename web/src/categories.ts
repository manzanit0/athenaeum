import type { Tally } from "./api";

/** What divides the levels of a category, as in "History / Military". */
export const separator = " / ";

/** Writes a category the way the library keeps it: levels trimmed and joined by the separator. */
export function tidy(name: string): string {
  return name
    .split("/")
    .map((level) => level.trim())
    .filter(Boolean)
    .join(separator);
}

/** Reports whether two categories are the same, whatever their case. */
export function same(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

/** Reports whether tag is the category parent or one below it. */
export function under(tag: string, parent: string): boolean {
  return same(tag, parent) || tag.toLowerCase().startsWith(parent.toLowerCase() + separator);
}

/** Returns the name of a category without the level above it. */
export function leaf(tag: string): string {
  const at = tag.indexOf(separator);
  return at < 0 ? tag : tag.slice(at + separator.length);
}

/** Adds a category to those of a book, which keeps only the most specific one of a branch. */
export function withCategory(list: string[], tag: string): string[] {
  return [...list.filter((entry) => !under(tag, entry) && !under(entry, tag)), tag];
}

/** Leaves out the categories that another one of the list is below, keeping the most specific of each branch. */
export function specific(list: string[]): string[] {
  return list.filter((entry) => !list.some((other) => !same(other, entry) && under(other, entry)));
}

/** A top-level category with the categories below it. */
export interface Branch {
  path: string;
  count: number;
  children: Tally[];
}

/**
 * Arranges categories as top-level branches with the categories below
 * them. The branches with the most books in weights come first, so their
 * order does not change while the shelf is narrowed.
 */
export function branches(tags: Tally[], weights: Tally[]): Branch[] {
  const found = new Map<string, Branch>();
  const branch = (path: string) => {
    const key = path.toLowerCase();
    if (!found.has(key)) {
      found.set(key, { path, count: 0, children: [] });
    }
    return found.get(key)!;
  };
  for (const tag of tags) {
    const at = tag.value.indexOf(separator);
    if (at < 0) {
      branch(tag.value).count = tag.count;
    } else {
      branch(tag.value.slice(0, at)).children.push(tag);
    }
  }
  const weight = (path: string) => weights.find((entry) => same(entry.value, path))?.count ?? 0;
  return [...found.values()].sort((a, b) => weight(b.path) - weight(a.path) || a.path.localeCompare(b.path));
}
