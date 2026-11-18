import type { ReactNode } from "react";
import type { Book } from "./api";
import { leaf } from "./categories";
import { byline, languageName, publishedOn } from "./format";
import { Link } from "./router";
import { sortBy } from "./shelf";

const cell = "truncate px-2.5 py-2";

/**
 * The heading of a column the shelf can be put in order of. Choosing it
 * does what choosing the same property under "Sort by" does, and the
 * arrow shows the direction only on the column the shelf is sorted by.
 */
function Heading({
  field,
  sort,
  onSort,
  className = "",
  children,
}: {
  field: string;
  sort: string;
  onSort: (sort: string) => void;
  className?: string;
  children: ReactNode;
}) {
  const [current, order] = sort.split(":");
  const active = current === field;
  return (
    <th
      scope="col"
      aria-sort={active ? (order === "asc" ? "ascending" : "descending") : "none"}
      className={`${cell} ${className}`}
    >
      <button
        type="button"
        onClick={() => onSort(sortBy(sort, field))}
        className="inline-flex items-baseline gap-1.5 font-semibold hover:text-ribbon-deep"
      >
        {children}
        <span aria-hidden className={active ? "text-ribbon-deep" : "text-soft/40"}>
          {active && order === "desc" ? "↓" : "↑"}
        </span>
      </button>
    </th>
  );
}

/** Formats the day a book was added. */
function day(value: string): string {
  return new Date(value).toLocaleDateString(undefined, { dateStyle: "medium" });
}

/**
 * The books of the shelf as a list, one line each, which the headings of
 * its columns put in order. Text too long for its column is cut short
 * with an ellipsis and shown in full on hover. The wider the list, the
 * more it tells: the author joins the title first, then the categories,
 * the publisher and, on wide screens, the language and the day the book
 * was added. The categories take the width the other columns leave.
 */
export function ShelfTable({
  books,
  sort,
  onSort,
  ticked,
  onTick,
}: {
  books: Book[];
  sort: string;
  onSort: (sort: string) => void;
  ticked: Set<number>;
  onTick?: (id: number) => void;
}) {
  const author = "hidden @md:table-cell";
  const categories = "hidden @2xl:table-cell";
  const publisher = "hidden @4xl:table-cell";
  const extra = "hidden @7xl:table-cell";
  return (
    <div className="@container">
      <table className="w-full table-fixed border-collapse text-left">
        <thead>
          <tr className="border-b border-ink">
            {onTick && (
              <th scope="col" className="w-9 py-2">
                <span className="sr-only">Select</span>
              </th>
            )}
            <Heading field="title" sort={sort} onSort={onSort} className="@2xl:w-[30%] @4xl:w-[26%]">
              Title
            </Heading>
            <Heading field="author" sort={sort} onSort={onSort} className={`${author} w-[35%] @2xl:w-[20%] @4xl:w-[17%] @7xl:w-[15%]`}>
              Author
            </Heading>
            <Heading field="published" sort={sort} onSort={onSort} className="w-[7.5rem]">
              Published
            </Heading>
            <Heading field="publisher" sort={sort} onSort={onSort} className={`${publisher} w-[14%] @7xl:w-[12%]`}>
              Publisher
            </Heading>
            <th scope="col" className={`${cell} ${categories} font-semibold`}>
              Categories
            </th>
            <Heading field="language" sort={sort} onSort={onSort} className={`${extra} w-[7.5rem]`}>
              Language
            </Heading>
            <Heading field="added" sort={sort} onSort={onSort} className={`${extra} w-[8rem]`}>
              Added
            </Heading>
            <th scope="col" className={`${cell} w-[5.5rem] pr-0 font-semibold @4xl:w-[7.5rem]`}>
              Files
            </th>
          </tr>
        </thead>
        <tbody>
          {books.map((book) => (
            <tr key={book.id} className="border-b border-rule hover:bg-sheet/60">
              {onTick && (
                <td className="py-2">
                  <input
                    type="checkbox"
                    checked={ticked.has(book.id)}
                    onChange={() => onTick(book.id)}
                    aria-label={`Select ${book.title}`}
                    className="block size-[1.05rem] accent-ribbon"
                  />
                </td>
              )}
              <td className={cell} title={book.title}>
                <Link
                  to={`/books/${book.id}`}
                  aria-label={`${book.title} by ${byline(book.authors)}`}
                  className="font-medium underline-offset-4 hover:underline"
                >
                  {book.title}
                </Link>
              </td>
              <td className={`${cell} ${author} text-soft`} title={byline(book.authors)}>
                {byline(book.authors)}
              </td>
              <td className={`${cell} tabular-nums text-soft`}>{publishedOn(book.published)}</td>
              <td className={`${cell} ${publisher} text-soft`} title={book.publisher}>
                {book.publisher}
              </td>
              <td className={`${cell} ${categories} text-soft`} title={book.tags.join(", ")}>
                {book.tags.map(leaf).join(", ")}
              </td>
              <td className={`${cell} ${extra} text-soft`}>{book.language && languageName(book.language)}</td>
              <td className={`${cell} ${extra} tabular-nums text-soft`}>{day(book.created_at)}</td>
              <td className={`${cell} pr-0 text-[0.87rem] text-soft`}>
                {book.formats.map((file) => file.format.toUpperCase()).join(", ") || "No files"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
