import { specific } from "./categories";

/**
 * The properties the shelf can be put in order of. Each goes by one name
 * wherever it is offered, and dates start with the newest.
 */
export const sortFields = [
  { field: "title", label: "Title", dated: false },
  { field: "author", label: "Author", dated: false },
  { field: "published", label: "Published", dated: true },
  { field: "publisher", label: "Publisher", dated: false },
  { field: "language", label: "Language", dated: false },
  { field: "added", label: "Added", dated: true },
  { field: "updated", label: "Changed", dated: true },
];

/** Reports whether the property called field is a date. */
function dated(field: string): boolean {
  return sortFields.some((entry) => entry.field === field && entry.dated);
}

/** Reports whether sort names a property to order by and a direction. */
function sortKnown(sort: string): boolean {
  const [field, order] = sort.split(":");
  return sortFields.some((entry) => entry.field === field) && (order === "asc" || order === "desc");
}

/** Returns the order by a property in its usual direction: A to Z, or the newest first. */
export function sortOf(field: string): string {
  return `${field}:${dated(field) ? "desc" : "asc"}`;
}

/** Returns the same order in the other direction. */
export function reversed(sort: string): string {
  const [field, order] = sort.split(":");
  return `${field}:${order === "asc" ? "desc" : "asc"}`;
}

/** Returns the order that choosing a property leads to: its usual direction, or the other one when it is the order already. */
export function sortBy(current: string, field: string): string {
  return current.split(":")[0] === field ? reversed(current) : sortOf(field);
}

/** Names the direction of an order for people. */
export function directionLabel(sort: string): string {
  const [field, order] = sort.split(":");
  if (dated(field)) {
    return order === "desc" ? "Newest first" : "Oldest first";
  }
  return order === "asc" ? "A to Z" : "Z to A";
}

/** How the books on the shelf are laid out. */
export type Layout = "grid" | "list";

/** Returns the layout the reader chose last, which the browser keeps. */
export function readLayout(): Layout {
  try {
    return window.localStorage.getItem("athenaeum.layout") === "list" ? "list" : "grid";
  } catch {
    return "grid";
  }
}

/** Keeps the layout the reader chose for the next visit. */
export function saveLayout(layout: Layout): void {
  try {
    window.localStorage.setItem("athenaeum.layout", layout);
  } catch {
    return;
  }
}

/**
 * What the shelf shows: a search, an order and what it is narrowed to.
 * A book needs one of the tags, one of the languages and one of the
 * formats that are named; untagged lets books without tags in as well.
 * The tags never hold a category together with one below it: the more
 * specific one is what the shelf is narrowed to.
 */
export interface View {
  query: string;
  sort: string;
  tags: string[];
  untagged: boolean;
  author: string;
  languages: string[];
  formats: string[];
}

/** The whole library in its usual order. */
export const blank: View = {
  query: "",
  sort: sortOf(sortFields[0].field),
  tags: [],
  untagged: false,
  author: "",
  languages: [],
  formats: [],
};

/** Reads what the shelf shows from the query string of the page. */
export function readView(search: string): View {
  const params = new URLSearchParams(search);
  const sort = params.get("sort") ?? "";
  return {
    query: params.get("q") ?? "",
    sort: sortKnown(sort) ? sort : blank.sort,
    tags: specific(params.getAll("tag")),
    untagged: params.get("untagged") === "true",
    author: params.get("author") ?? "",
    languages: params.getAll("language"),
    formats: params.getAll("format"),
  };
}

/** Lists the search and the narrowing of a view as query parameters. */
function parameters(view: View): URLSearchParams {
  const params = new URLSearchParams();
  if (view.query) {
    params.set("q", view.query);
  }
  view.tags.forEach((tag) => params.append("tag", tag));
  if (view.untagged) {
    params.set("untagged", "true");
  }
  if (view.author) {
    params.set("author", view.author);
  }
  view.languages.forEach((language) => params.append("language", language));
  view.formats.forEach((format) => params.append("format", format));
  return params;
}

/** Returns the address of the library page showing the given view. */
export function viewHref(view: View): string {
  const params = parameters(view);
  if (view.sort !== blank.sort) {
    params.set("sort", view.sort);
  }
  const query = params.toString();
  return query ? `/?${query}` : "/";
}

/** Returns the query string that asks the API for the books of a view. */
export function listQuery(view: View): string {
  const params = parameters(view);
  const [field, order] = view.sort.split(":");
  params.set("sort", field);
  params.set("order", order);
  return params.toString();
}

/** Reports whether a view leaves books out by tag, author, language or format. */
export function narrowed(view: View): boolean {
  return (
    view.tags.length > 0 || view.untagged || view.author !== "" || view.languages.length > 0 || view.formats.length > 0
  );
}

let last = "/";

/** Remembers the address of the shelf as it was last shown. */
export function rememberShelf(href: string): void {
  last = href;
}

/** Returns the address of the shelf as it was last shown. */
export function shelfHref(): string {
  return last;
}
