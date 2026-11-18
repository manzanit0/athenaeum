import { type DragEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { api, type Book, type Tally } from "../api";
import { Cover } from "../Cover";
import { byline, count, languageName } from "../format";
import { useDebounced, useLoad } from "../hooks";
import { Narrow } from "../Narrow";
import { useNotices } from "../notices";
import { Link, navigate, replace, useSearch } from "../router";
import {
  blank,
  type Layout,
  narrowed,
  readLayout,
  readView,
  directionLabel,
  rememberShelf,
  reversed,
  saveLayout,
  sortFields,
  sortOf,
  type View,
  viewHref,
} from "../shelf";
import { ShelfTable } from "../ShelfTable";
import { useSystem } from "../system";
import { Button, buttonClass, Empty, PageHeader, Problem } from "../ui";

/** One book on the shelf, which opens it or, while selecting, ticks it. */
function ShelfBook({ book, ticked, onTick }: { book: Book; ticked?: boolean; onTick?: () => void }) {
  const formats = book.formats.map((f) => f.format.toUpperCase()).join(", ");
  const caption = (
    <>
      <p className="mt-3 line-clamp-2 font-display text-[1.02rem] font-medium leading-snug">{book.title}</p>
      <p className="mt-0.5 truncate text-[0.9rem] text-soft">{byline(book.authors)}</p>
      <p className="mt-0.5 text-[0.8rem] text-soft">{formats || "No files"}</p>
    </>
  );
  if (onTick) {
    return (
      <li>
        <label className="block cursor-pointer">
          <span className={`relative block ${ticked ? "outline-3 outline-offset-3 outline-ribbon" : ""}`}>
            <Cover book={book} className={ticked ? "" : "opacity-80"} />
            <input
              type="checkbox"
              checked={ticked}
              onChange={onTick}
              aria-label={`Select ${book.title}`}
              className="absolute left-2 top-2 size-5 accent-ribbon"
            />
          </span>
          {caption}
        </label>
      </li>
    );
  }
  return (
    <li>
      <Link to={`/books/${book.id}`} aria-label={`${book.title} by ${byline(book.authors)}`} className="group block">
        <Cover book={book} className="transition-transform duration-150 group-hover:-translate-y-1" />
        {caption}
      </Link>
    </li>
  );
}

/** What the shelf is narrowed to, each part removable on its own. */
function Narrowing({ view, onShow }: { view: View; onShow: (view: View) => void }) {
  const parts = [
    ...view.tags.map((tag) => ({ label: tag, without: { ...view, tags: view.tags.filter((entry) => entry !== tag) } })),
    ...(view.untagged ? [{ label: "Without a category", without: { ...view, untagged: false } }] : []),
    ...(view.author ? [{ label: `By ${view.author}`, without: { ...view, author: "" } }] : []),
    ...view.languages.map((language) => ({
      label: languageName(language),
      without: { ...view, languages: view.languages.filter((entry) => entry !== language) },
    })),
    ...view.formats.map((format) => ({
      label: format.toUpperCase(),
      without: { ...view, formats: view.formats.filter((entry) => entry !== format) },
    })),
  ];
  return (
    <div role="group" aria-label="Showing only" className="mb-6 flex flex-wrap items-center gap-2">
      <span className="text-soft">Showing only</span>
      {parts.map(({ label, without }) => (
        <button
          key={label}
          type="button"
          aria-label={`Stop narrowing to ${label}`}
          onClick={() => onShow(without)}
          className="inline-flex items-center gap-2 rounded-sm border border-ink px-2.5 py-1 leading-tight hover:bg-ink/5"
        >
          {label}
          <span aria-hidden className="text-soft">
            ×
          </span>
        </button>
      ))}
      <Button variant="quiet" onClick={() => onShow({ ...blank, query: view.query, sort: view.sort })}>
        Show everything
      </Button>
    </div>
  );
}

/** The bar that files the ticked books under a category, or takes them out of one. */
function Tagging({
  ticked,
  shown,
  known,
  onAll,
  onNone,
  onTag,
  onDone,
}: {
  ticked: number;
  shown: number;
  known: Tally[];
  onAll: () => void;
  onNone: () => void;
  onTag: (add: string[], remove: string[]) => Promise<unknown>;
  onDone: () => void;
}) {
  const id = useId();
  const [tag, setTag] = useState("");
  const name = tag.trim();
  const ready = ticked > 0 && name !== "";
  return (
    <div
      role="region"
      aria-label="Selected books"
      className="sticky bottom-4 z-10 mt-10 flex flex-wrap items-end gap-x-6 gap-y-3 rounded-sm border border-ink bg-sheet px-4 py-3 shadow-[0_10px_24px_-12px_rgb(27_33_30/0.6)]"
    >
      <div>
        <p className="font-semibold">{ticked === 0 ? "Tick the books to file" : `${count(ticked, "book")} selected`}</p>
        <Button variant="quiet" onClick={ticked === shown ? onNone : onAll}>
          {ticked === shown ? "Select none" : `Select all ${shown} shown`}
        </Button>
      </div>
      <div className="min-w-[12rem] flex-1">
        <label htmlFor={id} className="mb-1 block font-medium">
          Category
        </label>
        <input
          id={id}
          list={`${id}-known`}
          value={tag}
          onChange={(event) => setTag(event.target.value)}
          placeholder="History / Military"
          className="w-full rounded-sm border border-rule bg-paper px-3 py-2 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
        />
        <datalist id={`${id}-known`}>
          {known.map((entry) => (
            <option key={entry.value} value={entry.value} />
          ))}
        </datalist>
      </div>
      <div className="flex gap-2">
        <Button variant="primary" disabled={!ready} onClick={() => onTag([name], []).then(() => setTag(""))}>
          Add category
        </Button>
        <Button disabled={!ready} onClick={() => onTag([], [name]).then(() => setTag(""))}>
          Remove category
        </Button>
        <Button variant="quiet" className="ml-3" onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}

/** The shelf of the library: search it, narrow it by category, add to it and file its books. */
export function LibraryPage() {
  const system = useSystem();
  const { notify, attempt } = useNotices();
  const search = useSearch();
  const view = useMemo(() => readView(search), [search]);
  const [typed, setTyped] = useState(view.query);
  const written = useRef(view.query);
  const settled = useDebounced(typed, 200);
  const [dragging, setDragging] = useState(false);
  const [adding, setAdding] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [selecting, setSelecting] = useState(false);
  const [ticked, setTicked] = useState<Set<number>>(new Set());
  const [layout, setLayout] = useState<Layout>(readLayout);
  const picker = useRef<HTMLInputElement>(null);
  const books = useLoad(() => api.books(view), [search]);
  const facets = useLoad(() => api.facets(view), [search]);
  const library = useLoad(() => api.facets(blank), []);

  useEffect(() => {
    if (settled !== view.query) {
      written.current = settled;
      replace(viewHref({ ...view, query: settled }));
    }
  }, [settled]);
  useEffect(() => {
    if (view.query !== written.current) {
      written.current = view.query;
      setTyped(view.query);
    }
  }, [view.query]);
  useEffect(() => rememberShelf(viewHref(view)), [view]);

  const show = (next: View) => {
    if (viewHref(next) !== viewHref(view)) {
      navigate(viewHref(next), !window.matchMedia("(min-width: 48rem)").matches);
    }
  };
  const reload = () => {
    books.reload();
    facets.reload();
    library.reload();
  };

  const add = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    setAdding(true);
    await attempt(async () => {
      const result = await api.importBooks(files);
      if (result.books.length > 0) {
        notify(`Added ${count(result.books.length, "book")}`);
      }
      for (const failure of result.errors) {
        notify(`${failure.file} was not added: ${failure.error}`, "error");
      }
      reload();
    });
    setAdding(false);
    if (picker.current) {
      picker.current.value = "";
    }
  };

  const drop = (event: DragEvent) => {
    event.preventDefault();
    setDragging(false);
    add(Array.from(event.dataTransfer.files));
  };

  const shelf = books.data ?? [];
  const chosen = shelf.filter((book) => ticked.has(book.id));
  const tick = (id: number) => {
    const next = new Set(ticked);
    if (!next.delete(id)) {
      next.add(id);
    }
    setTicked(next);
  };
  const tag = (put: string[], taken: string[]) =>
    attempt(async () => {
      await api.tagBooks(
        chosen.map((book) => book.id),
        put,
        taken,
      );
      const changed = count(chosen.length, "book");
      notify(put.length > 0 ? `Filed ${changed} under “${put[0]}”` : `Took ${changed} out of “${taken[0]}”`);
      reload();
    });

  const total = (facets.data ?? library.data)?.total ?? 0;
  const searching = view.query.trim() !== "";
  const partial = searching || narrowed(view);
  const lead = !books.data ? undefined : partial ? `${shelf.length} of ${count(total, "book")}` : count(shelf.length, "book");
  return (
    <div
      onDragOver={(event) => {
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={drop}
      className={dragging ? "outline-2 outline-offset-8 outline-dashed outline-ribbon" : ""}
    >
      <PageHeader title="Library" lead={lead}>
        {total > 0 && (
          <Button
            onClick={() => {
              setSelecting(!selecting);
              setTicked(new Set());
            }}
          >
            {selecting ? "Done" : "Categorise books"}
          </Button>
        )}
        <label className={`${buttonClass("primary")} cursor-pointer focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-ribbon`}>
          {adding ? "Adding…" : "Add books"}
          <input
            ref={picker}
            type="file"
            multiple
            className="sr-only"
            accept={system.import_formats.map((format) => `.${format}`).join(",")}
            onChange={(event) => add(Array.from(event.target.files ?? []))}
          />
        </label>
      </PageHeader>

      <div className="mb-6 flex flex-wrap items-end gap-4">
        <div className="min-w-[14rem] max-w-[44rem] flex-1">
          <label htmlFor="library-search" className="mb-1 block font-medium">
            Search library
          </label>
          <input
            id="library-search"
            type="search"
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            placeholder="Title, author, series, category or ISBN"
            className="w-full rounded-sm border border-rule bg-sheet px-3 py-2 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
          />
        </div>
        <div>
          <label htmlFor="library-sort" className="mb-1 block font-medium">
            Sort by
          </label>
          <div className="flex gap-2">
            <select
              id="library-sort"
              value={view.sort.split(":")[0]}
              onChange={(event) => show({ ...view, sort: sortOf(event.target.value) })}
              className="rounded-sm border border-rule bg-sheet px-3 py-2"
            >
              {sortFields.map(({ field, label }) => (
                <option key={field} value={field}>
                  {label}
                </option>
              ))}
            </select>
            <button
              type="button"
              title="Reverse the order"
              onClick={() => show({ ...view, sort: reversed(view.sort) })}
              className="inline-flex min-w-[8.25rem] items-baseline gap-1.5 rounded-sm border border-rule bg-sheet px-3 py-2 hover:border-ink"
            >
              <span aria-hidden className="text-ribbon-deep">
                {view.sort.endsWith(":asc") ? "↑" : "↓"}
              </span>
              {directionLabel(view.sort)}
            </button>
          </div>
        </div>
        <div role="group" aria-label="Show books as" className="flex">
          {(["grid", "list"] as const).map((option) => (
            <button
              key={option}
              type="button"
              aria-pressed={layout === option}
              onClick={() => {
                setLayout(option);
                saveLayout(option);
              }}
              className={
                "border px-3.5 py-2 font-medium first:rounded-l-sm last:-ml-px last:rounded-r-sm " +
                (layout === option ? "relative border-ink bg-ink text-sheet" : "border-rule bg-sheet hover:border-ink")
              }
            >
              {option === "grid" ? "Grid" : "List"}
            </button>
          ))}
        </div>
        {total > 0 && (
          <Button className="md:hidden" aria-expanded={choosing} onClick={() => setChoosing(!choosing)}>
            {choosing ? "Hide categories" : "Browse by category"}
          </Button>
        )}
      </div>

      {books.error && <Problem>The library could not be loaded: {books.error}</Problem>}
      {books.data && facets.data && total === 0 && (
        <Empty title="Your library is empty">
          Choose <strong className="font-semibold text-ink">Add books</strong> or drop files anywhere on this page.
          EPUB, MOBI, AZW3, PDF and {Math.max(system.import_formats.length - 4, 0)} other formats are accepted.
        </Empty>
      )}
      {total > 0 && (
        <div className="md:flex md:gap-9">
          <aside
            aria-label="Narrow the shelf"
            className={`md:block md:w-[13rem] md:shrink-0 ${choosing ? "" : "hidden"}`}
          >
            {facets.data && (
              <Narrow facets={facets.data} library={library.data} view={view} onShow={show} />
            )}
          </aside>
          <div className="min-w-0 flex-1">
            {narrowed(view) && <Narrowing view={view} onShow={show} />}
            {books.data && shelf.length === 0 && (
              <Empty title={searching ? `No book matches “${view.query.trim()}”` : "No book matches"}>
                {narrowed(view) ? (
                  <>
                    Nothing is left after narrowing.{" "}
                    <Button variant="quiet" onClick={() => show({ ...blank, query: view.query, sort: view.sort })}>
                      Show everything
                    </Button>
                  </>
                ) : (
                  "Try fewer words, or a different spelling."
                )}
              </Empty>
            )}
            {layout === "list" && shelf.length > 0 && (
              <ShelfTable
                books={shelf}
                sort={view.sort}
                onSort={(sort) => show({ ...view, sort })}
                ticked={ticked}
                onTick={selecting ? tick : undefined}
              />
            )}
            {layout === "grid" && (
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(8.75rem,1fr))] gap-x-7 gap-y-9">
                {shelf.map((book) => (
                  <ShelfBook
                    key={book.id}
                    book={book}
                    ticked={ticked.has(book.id)}
                    onTick={selecting ? () => tick(book.id) : undefined}
                  />
                ))}
              </ul>
            )}
            {selecting && (
              <Tagging
                ticked={chosen.length}
                shown={shelf.length}
                known={library.data?.tags ?? []}
                onAll={() => setTicked(new Set(shelf.map((book) => book.id)))}
                onNone={() => setTicked(new Set())}
                onTag={tag}
                onDone={() => setSelecting(false)}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
