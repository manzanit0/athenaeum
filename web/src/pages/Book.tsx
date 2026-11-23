import { type FormEvent, Fragment, useEffect, useId, useState } from "react";
import { api, type Book, downloadUrl, type Metadata, type Tally, warningsOf } from "../api";
import { same, tidy, withCategory } from "../categories";
import { Cover } from "../Cover";
import { byline, bytes, count, languageName, monthNames, publishedOn, splitList } from "../format";
import { useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Link, navigate } from "../router";
import { blank, shelfHref, viewHref } from "../shelf";
import { usable, useSystem } from "../system";
import { Button, buttonClass, Confirm, Field, Problem, Section, TextArea } from "../ui";

/** The metadata form values, with lists held as comma separated text. */
interface Draft {
  title: string;
  authors: string;
  series: string;
  seriesIndex: string;
  publisher: string;
  language: string;
  isbn: string;
  year: string;
  month: string;
  day: string;
  description: string;
}

/** Builds the form values for a book. */
function draftOf(book: Book): Draft {
  return {
    title: book.title,
    authors: book.authors.join(", "),
    series: book.series,
    seriesIndex: book.series_index ? String(book.series_index) : "",
    publisher: book.publisher,
    language: book.language,
    isbn: book.isbn,
    year: book.published.split("-")[0] ?? "",
    month: String(Number(book.published.split("-")[1] ?? "") || ""),
    day: String(Number(book.published.split("-")[2] ?? "") || ""),
    description: book.description,
  };
}

/**
 * Says what is wrong with the publication date in the form, if anything.
 * A year alone is a date, and so is a year with a month.
 */
function dateProblem(draft: Draft): string {
  const [year, month, day] = [draft.year, draft.month, draft.day].map(Number);
  if (!draft.year) {
    return draft.month || draft.day ? "The publication date must include a year." : "";
  }
  if (draft.year.length !== 4) {
    return "Write the year with four digits.";
  }
  if (!draft.day) {
    return "";
  }
  if (!draft.month) {
    return "The publication date must include a month before a day.";
  }
  const date = new Date(Date.UTC(year, month - 1, day));
  return date.getUTCMonth() === month - 1 && day > 0 ? "" : "The publication date must be a real date.";
}

/** Converts form values and the chosen categories back into metadata. */
function metadataOf(draft: Draft, tags: string[]): Metadata {
  return {
    title: draft.title.trim(),
    authors: splitList(draft.authors),
    series: draft.series.trim(),
    series_index: Number(draft.seriesIndex) || 0,
    publisher: draft.publisher.trim(),
    language: draft.language.trim(),
    isbn: draft.isbn.trim(),
    tags,
    published: [draft.year, draft.month.padStart(2, "0"), draft.day.padStart(2, "0")]
      .slice(0, draft.day && draft.month ? 3 : draft.month ? 2 : 1)
      .join("-"),
    description: draft.description.trim(),
  };
}

/** The most categories a book is filed under. */
const mostCategories = 3;

/**
 * The categories of a book: those it is filed under, each removable, a
 * list of the library's categories to add one from and a field to make a
 * new one. Adding a category replaces a less specific one of its branch.
 */
function CategoryPicker({
  value,
  known,
  onChange,
}: {
  value: string[];
  known: Tally[];
  onChange: (next: string[]) => void;
}) {
  const id = useId();
  const [fresh, setFresh] = useState("");
  const full = value.length >= mostCategories;
  const offered = known.filter((entry) => !value.some((tag) => same(tag, entry.value)));
  const add = (name: string) => {
    if (tidy(name)) {
      onChange(withCategory(value, tidy(name)));
      setFresh("");
    }
  };
  const field = "rounded-sm border border-rule bg-sheet px-3 py-2 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40";
  return (
    <fieldset className="min-w-0 sm:col-span-6">
      <legend className="mb-1 font-medium">Categories</legend>
      {value.length === 0 && <p className="text-soft">This book is not filed under a category yet.</p>}
      <ul className="flex flex-wrap gap-2">
        {value.map((tag) => (
          <li key={tag} className="inline-flex items-center gap-2 rounded-sm border border-ink py-1 pl-2.5 pr-1 leading-tight">
            {tag}
            <button
              type="button"
              aria-label={`Remove ${tag}`}
              onClick={() => onChange(value.filter((entry) => entry !== tag))}
              className="grid size-5 place-items-center rounded-sm text-soft hover:bg-ink/10 hover:text-ink"
            >
              <span aria-hidden>×</span>
            </button>
          </li>
        ))}
      </ul>
      <div className="mt-3 flex flex-wrap items-end gap-x-6 gap-y-3">
        <div>
          <label htmlFor={`${id}-known`} className="mb-1 block text-[0.93rem] text-soft">
            Add a category
          </label>
          <select
            id={`${id}-known`}
            value=""
            disabled={full || offered.length === 0}
            onChange={(event) => add(event.target.value)}
            className={`${field} max-w-full disabled:opacity-50`}
          >
            <option value="">Choose one</option>
            {offered.map((entry) => (
              <option key={entry.value} value={entry.value}>
                {entry.value}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor={`${id}-fresh`} className="mb-1 block text-[0.93rem] text-soft">
            New category
          </label>
          <div className="flex gap-2">
            <input
              id={`${id}-fresh`}
              value={fresh}
              disabled={full}
              placeholder="History / Military"
              onChange={(event) => setFresh(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  add(fresh);
                }
              }}
              className={`${field} w-[14rem] max-w-full disabled:opacity-50`}
            />
            <Button disabled={full || !tidy(fresh)} onClick={() => add(fresh)}>
              Add
            </Button>
          </div>
        </div>
      </div>
      <p className="mt-2 max-w-[62ch] text-[0.87rem] text-soft">
        A book takes up to three categories, the most specific ones. Write a category below another as Parent / Name.
      </p>
    </fieldset>
  );
}

/**
 * The publication date as three parts, of which only the year is needed:
 * the year typed in digits, the month chosen by name, so that it is never
 * taken for the day, and the day, which waits for a month.
 */
function PublishedField({
  draft,
  problem,
  onChange,
}: {
  draft: Draft;
  problem: string;
  onChange: (part: Partial<Draft>) => void;
}) {
  const id = useId();
  const digits = (value: string, most: number) => value.replace(/\D/g, "").slice(0, most);
  const part = "rounded-sm border bg-sheet px-3 py-2 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40";
  const edge = problem ? "border-ribbon" : "border-rule";
  const label = "mb-1 block text-[0.87rem] text-soft";
  return (
    <fieldset className="min-w-0 sm:col-span-6" aria-describedby={`${id}-hint`}>
      <legend className="mb-1 font-medium">Published</legend>
      <div className="flex gap-2">
        <div>
          <label htmlFor={`${id}-year`} className={label}>
            Year
          </label>
          <input
            id={`${id}-year`}
            inputMode="numeric"
            autoComplete="off"
            placeholder="2019"
            value={draft.year}
            onChange={(event) => onChange({ year: digits(event.target.value, 4) })}
            className={`${part} ${edge} w-[5.25rem]`}
          />
        </div>
        <div className="min-w-0 w-[11rem]">
          <label htmlFor={`${id}-month`} className={label}>
            Month
          </label>
          <select
            id={`${id}-month`}
            value={draft.month}
            onChange={(event) => onChange({ month: event.target.value, ...(event.target.value ? {} : { day: "" }) })}
            className={`${part} ${edge} w-full`}
          >
            <option value="">Not known</option>
            {monthNames().map((name, index) => (
              <option key={name} value={String(index + 1)}>
                {name}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor={`${id}-day`} className={label}>
            Day
          </label>
          <input
            id={`${id}-day`}
            inputMode="numeric"
            autoComplete="off"
            disabled={!draft.month}
            value={draft.day}
            onChange={(event) => onChange({ day: digits(event.target.value, 2) })}
            className={`${part} ${edge} w-[3.75rem] disabled:opacity-45`}
          />
        </div>
      </div>
      {problem ? (
        <p id={`${id}-hint`} role="alert" className="mt-1 text-[0.87rem] text-ribbon-deep">
          {problem}
        </p>
      ) : (
        <p id={`${id}-hint`} className="mt-1 text-[0.87rem] text-soft">
          The year alone is enough.
        </p>
      )}
    </fieldset>
  );
}

/**
 * The form that edits a book's metadata. It tells its page whether it
 * holds changes that have not been saved.
 */
function DetailsForm({
  book,
  onSaved,
  onDirty,
}: {
  book: Book;
  onSaved: (book: Book) => void;
  onDirty: (dirty: boolean) => void;
}) {
  const { notify, attempt } = useNotices();
  const [draft, setDraft] = useState(() => draftOf(book));
  const [saving, setSaving] = useState(false);
  const [tags, setTags] = useState(book.tags);
  const [checked, setChecked] = useState(false);
  const problem = dateProblem(draft);
  const library = useLoad(() => api.facets(blank), [book]);
  const dirty = JSON.stringify([draft, tags]) !== JSON.stringify([draftOf(book), book.tags]);
  useEffect(() => {
    setDraft(draftOf(book));
    setTags(book.tags);
  }, [book]);
  useEffect(() => onDirty(dirty), [dirty]);
  const bind = (key: keyof Draft) => ({
    value: draft[key],
    onChange: (event: { target: { value: string } }) => setDraft({ ...draft, [key]: event.target.value }),
  });
  const save = async (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (problem) {
      return;
    }
    setSaving(true);
    await attempt(async () => {
      onSaved(await api.updateBook(book.id, metadataOf(draft, tags)));
      notify("Changes saved");
    });
    setSaving(false);
  };
  return (
    <form onSubmit={save} className="grid gap-x-5 gap-y-4 sm:grid-cols-6">
      <Field label="Title" required className="sm:col-span-6" {...bind("title")} />
      <Field label="Authors" hint="Separate several authors with commas." className="sm:col-span-6" {...bind("authors")} />
      <Field label="Series" className="sm:col-span-4" {...bind("series")} />
      <Field label="Number in series" inputMode="decimal" className="sm:col-span-2" {...bind("seriesIndex")} />
      <Field label="Publisher" className="sm:col-span-6" {...bind("publisher")} />
      <PublishedField draft={draft} problem={checked ? problem : ""} onChange={(part) => setDraft({ ...draft, ...part })} />
      <Field label="Language" placeholder="en" className="sm:col-span-2" {...bind("language")} />
      <Field label="ISBN" className="sm:col-span-4" {...bind("isbn")} />
      <CategoryPicker value={tags} known={library.data?.tags ?? []} onChange={setTags} />
      <TextArea label="Description" className="sm:col-span-6" {...bind("description")} />
      <div className="sm:col-span-6">
        <Button type="submit" variant="primary" disabled={saving}>
          {saving ? "Saving…" : "Save changes"}
        </Button>
      </div>
    </form>
  );
}

/**
 * What is known about a book, as text: its description and the details
 * that are filled in.
 */
function About({ book }: { book: Book }) {
  const series = book.series && book.series_index ? `${book.series}, book ${book.series_index}` : book.series;
  const facts = [
    ["Series", series],
    ["Publisher", book.publisher],
    ["Published", publishedOn(book.published)],
    ["Language", book.language && languageName(book.language)],
    ["ISBN", book.isbn],
  ].filter(([, value]) => value);
  if (!book.description && facts.length === 0) {
    return <p className="mb-10 text-soft">Nothing has been written down about this book yet.</p>;
  }
  return (
    <div className="mb-10">
      {book.description && (
        <p className="max-w-[62ch] whitespace-pre-line text-[1.06rem] leading-relaxed">{book.description}</p>
      )}
      {facts.length > 0 && (
        <dl className={`grid max-w-[62ch] grid-cols-[7.5rem_minmax(0,1fr)] gap-y-1.5 ${book.description ? "mt-6" : ""}`}>
          {facts.map(([name, value]) => (
            <Fragment key={name}>
              <dt className="text-soft">{name}</dt>
              <dd>{value}</dd>
            </Fragment>
          ))}
        </dl>
      )}
    </div>
  );
}

/**
 * What to do with the files the library only keeps. A Pages document
 * is one: only the Pages app opens it, so that is where it is turned
 * into a file the library can read, convert and send to the Kindle.
 */
function KeptNote({ formats }: { formats: string[] }) {
  const names = formats.map((format) => format.toUpperCase()).join(" and ");
  return (
    <p className="mt-3 max-w-[62ch] text-[0.9rem] text-soft">
      The {names} file is kept as it is: it can be downloaded, but not read here, converted or sent to the Kindle.
      {formats.includes("pages") &&
        " To get a file that can, open it in the Pages app, export it as EPUB or Word, and add that file to this book."}
    </p>
  );
}

/**
 * The list of a book's files, each of which can be downloaded. While
 * the book is being edited, files can also be removed, converted and
 * added.
 */
function Files({ book, editing, onChanged }: { book: Book; editing: boolean; onChanged: (book?: Book) => void }) {
  const system = useSystem();
  const { notify, track, attempt } = useNotices();
  const builtIn = (target: string) =>
    book.formats.some((file) => system.native_conversions[file.format]?.includes(target));
  const kept = book.formats.filter((file) => system.kept_formats.includes(file.format));
  const targets = system.output_formats.filter(
    (format) =>
      usable(book, system) &&
      !book.formats.some((f) => f.format === format) &&
      (system.can_convert || builtIn(format)),
  );
  const [target, setTarget] = useState("");
  const chosen = targets.includes(target) ? target : (targets[0] ?? "");

  const convert = () =>
    attempt(async () => {
      const job = await api.convert(book.id, chosen);
      track(job, {
        done: (finished) => {
          const left = warningsOf(finished).length;
          const note = left > 0 ? `, with ${count(left, "thing")} left out. Activity has the list` : "";
          return `Converted to ${chosen.toUpperCase()}${note}`;
        },
        onDone: () => onChanged(),
      });
    });
  const remove = (format: string) =>
    attempt(async () => {
      onChanged(await api.deleteFormat(book.id, format));
      notify(`Removed the ${format.toUpperCase()} file`);
    });
  const add = (files: File[]) =>
    attempt(async () => {
      onChanged(await api.addFormat(book.id, files));
      notify("File added");
    });

  return (
    <Section title="Files">
      {book.formats.length === 0 && <p className="text-soft">This book has no files yet.</p>}
      <ul className="divide-y divide-rule border-y border-rule">
        {book.formats.map((file) => (
          <li key={file.format} className="flex flex-wrap items-center gap-x-5 gap-y-1 py-3">
            <span className="w-14 font-semibold">{file.format.toUpperCase()}</span>
            <span className="min-w-0 flex-1 truncate text-soft">{file.filename}</span>
            <span className="text-soft tabular-nums">{bytes(file.size)}</span>
            <a
              href={downloadUrl(book, file.format)}
              aria-label={`Download ${file.format.toUpperCase()}`}
              className="underline underline-offset-4 hover:text-ribbon-deep"
            >
              Download
            </a>
            {editing && (
              <Button variant="quiet" aria-label={`Remove ${file.format.toUpperCase()}`} onClick={() => remove(file.format)}>
                Remove
              </Button>
            )}
          </li>
        ))}
      </ul>
      {kept.length > 0 && <KeptNote formats={kept.map((file) => file.format)} />}
      {editing && (
        <>
          <div className="mt-5 flex flex-wrap items-end gap-x-8 gap-y-4">
            <div>
              <label htmlFor="convert-target" className="mb-1 block font-medium">
                Convert to
              </label>
              <div className="flex gap-2">
                <select
                  id="convert-target"
                  value={chosen}
                  onChange={(event) => setTarget(event.target.value)}
                  disabled={targets.length === 0}
                  className="rounded-sm border border-rule bg-sheet px-3 py-2"
                >
                  {targets.map((format) => (
                    <option key={format} value={format}>
                      {format.toUpperCase()}
                    </option>
                  ))}
                </select>
                <Button onClick={convert} disabled={!chosen || book.formats.length === 0}>
                  Convert
                </Button>
              </div>
            </div>
            <label className={`${buttonClass()} cursor-pointer focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-ribbon`}>
              Add a file
              <input
                type="file"
                className="sr-only"
                accept={system.import_formats.map((format) => `.${format}`).join(",")}
                onChange={(event) => {
                  add(Array.from(event.target.files ?? []));
                  event.target.value = "";
                }}
              />
            </label>
          </div>
          {!system.can_convert && system.version && (
            <p className="mt-3 max-w-[62ch] text-[0.9rem] text-soft">
              EPUB and AZW3 convert to each other, and PDF and Word documents to EPUB or AZW3, without anything
              else installed. Other conversions need calibre's ebook-convert, which was not found on this machine.
              The Docker image includes it.
            </p>
          )}
        </>
      )}
    </Section>
  );
}

/**
 * The cover of a book with the actions that apply to the whole book. A
 * book that has files opens for reading from its cover, which says so
 * under the pointer, on keyboard focus and always on touch screens.
 * Changing the cover and deleting the book wait for the book to be
 * edited.
 */
function Sidebar({ book, editing, onChanged }: { book: Book; editing: boolean; onChanged: (book?: Book) => void }) {
  const system = useSystem();
  const { notify, track, attempt } = useNotices();
  const device = useLoad(api.device, []);
  const [confirming, setConfirming] = useState(false);
  const connected = device.data?.connected ?? false;
  const openable = usable(book, system);

  const send = () =>
    attempt(async () => {
      const job = await api.sendToDevice(book.id);
      track(job, { done: `Sent “${book.title}” to the Kindle`, onDone: () => onChanged() });
    });
  const setCover = (file?: File) =>
    file &&
    attempt(async () => {
      onChanged(await api.setCover(book.id, file));
      notify("Cover changed");
    });
  const removeCover = () =>
    attempt(async () => {
      onChanged(await api.removeCover(book.id));
      notify("Cover removed");
    });
  const remove = () =>
    attempt(async () => {
      await api.deleteBook(book.id);
      notify(`Deleted “${book.title}”`);
      navigate(shelfHref());
    });

  return (
    <div className="mx-auto w-full max-w-[15rem] md:mx-0">
      {openable ? (
        <Link to={`/books/${book.id}/preview`} aria-label={`Read ${book.title}`} className="cover-action relative block">
          <Cover book={book} />
          <span aria-hidden className="cover-scrim absolute inset-0 rounded-l-[2px] rounded-r-[5px] bg-ink/35" />
          <span aria-hidden className="cover-cue absolute inset-0 grid place-items-center">
            <span className="rounded-sm bg-ink px-5 py-2 font-medium text-sheet shadow-[0_6px_16px_-6px_rgb(27_33_30/0.7)]">
              Read
            </span>
          </span>
        </Link>
      ) : (
        <Cover book={book} />
      )}
      <div className="mt-6 flex flex-col gap-2.5">
        <Button variant="primary" onClick={send} disabled={!connected || !openable}>
          Send to Kindle
        </Button>
        {device.data && !connected && (
          <p className="text-[0.87rem] text-soft">
            No Kindle is connected.{" "}
            <Link to="/kindle" className="underline underline-offset-2">
              Set it up
            </Link>
          </p>
        )}
        {editing && (
          <>
            <label className={`${buttonClass()} cursor-pointer focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-ribbon`}>
              Change cover
              <input
                type="file"
                accept="image/jpeg,image/png,image/gif,image/webp"
                className="sr-only"
                onChange={(event) => {
                  setCover(event.target.files?.[0]);
                  event.target.value = "";
                }}
              />
            </label>
            {book.cover && (
              <Button variant="quiet" className="self-start" onClick={removeCover}>
                Remove cover
              </Button>
            )}
            <Button variant="danger" className="mt-4" onClick={() => setConfirming(true)}>
              Delete book
            </Button>
          </>
        )}
      </div>
      <Confirm
        open={confirming}
        title={`Delete “${book.title}”?`}
        action="Delete book"
        onCancel={() => setConfirming(false)}
        onConfirm={() => {
          setConfirming(false);
          remove();
        }}
      >
        The book and all its files are removed from the library. Copies on the Kindle stay where they are.
      </Confirm>
    </div>
  );
}

/**
 * One book. The page says what the book is and offers what a reader
 * does with it: read it, send it to the Kindle, download a file. Its
 * details, cover and files are changed, and the book deleted, in a
 * second view of the same page, which "Edit book" opens. Saving the
 * details there comes back to the book; leaving with details unsaved
 * asks first.
 */
export function BookPage({ id, editing }: { id: number; editing: boolean }) {
  const book = useLoad(() => api.book(id), [id]);
  const [dirty, setDirty] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const changed = (next?: Book) => (next ? book.set(next) : book.reload());
  const saved = (next: Book) => {
    book.set(next);
    setDirty(false);
    navigate(`/books/${id}`);
  };
  if (book.error) {
    return (
      <>
        <Problem>This book could not be opened: {book.error}</Problem>
        <Link to="/" className="mt-4 inline-block underline underline-offset-4">
          Back to the library
        </Link>
      </>
    );
  }
  if (!book.data) {
    return <p className="text-soft">Opening the book…</p>;
  }
  return (
    <>
      <Link to={shelfHref()} className="text-soft underline underline-offset-4 hover:text-ink">
        Back to the library
      </Link>
      <header className="mb-8 mt-3 flex flex-wrap items-end justify-between gap-x-6 gap-y-4 border-b border-rule pb-5">
        <div className="min-w-0">
          <h1 className="font-display text-[2.1rem] font-medium leading-tight tracking-[-0.01em]">{book.data.title}</h1>
          <p className="mt-1 text-[1.1rem] text-soft">
            {book.data.authors.length === 0 && byline(book.data.authors)}
            {book.data.authors.map((author, index) => (
              <Fragment key={author}>
                {index > 0 && ", "}
                <Link
                  to={viewHref({ ...blank, author })}
                  aria-label={`Books by ${author}`}
                  className="underline-offset-4 hover:text-ink hover:underline"
                >
                  {author}
                </Link>
              </Fragment>
            ))}
          </p>
          {book.data.tags.length > 0 && (
            <ul aria-label="Filed under" className="mt-3 flex flex-wrap gap-2">
              {book.data.tags.map((tag) => (
                <li key={tag}>
                  <Link
                    to={viewHref({ ...blank, tags: [tag] })}
                    aria-label={`Books in ${tag}`}
                    className="block rounded-sm border border-rule px-2.5 py-0.5 text-[0.93rem] hover:border-ink"
                  >
                    {tag}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
        {editing ? (
          <Link
            to={`/books/${id}`}
            className={buttonClass()}
            onClick={(event) => {
              if (dirty) {
                event.preventDefault();
                setLeaving(true);
              }
            }}
          >
            Done
          </Link>
        ) : (
          <Link to={`/books/${id}/edit`} className={buttonClass()}>
            Edit book
          </Link>
        )}
      </header>
      <div className="grid gap-x-12 gap-y-10 md:grid-cols-[15rem_minmax(0,1fr)]">
        <Sidebar book={book.data} editing={editing} onChanged={changed} />
        <div className="min-w-0">
          {editing ? (
            <Section title="Details">
              <DetailsForm book={book.data} onSaved={saved} onDirty={setDirty} />
            </Section>
          ) : (
            <About book={book.data} />
          )}
          <Files book={book.data} editing={editing} onChanged={changed} />
        </div>
      </div>
      <Confirm
        open={leaving}
        title="Leave without saving?"
        action="Discard changes"
        onCancel={() => setLeaving(false)}
        onConfirm={() => {
          setLeaving(false);
          setDirty(false);
          navigate(`/books/${id}`);
        }}
      >
        The changes to the details of this book have not been saved.
      </Confirm>
    </>
  );
}
