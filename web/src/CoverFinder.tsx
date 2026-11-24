import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { api, type Book, type CoverCandidate, type CoverFailure, type CoverQuery, type CoverSearch, foundCoverUrl } from "./api";
import { Cover } from "./Cover";
import { messageOf } from "./format";
import { Button, Empty, Field, Problem } from "./ui";

/** Where a search for covers stands. */
type Search = { state: "searching" } | { state: "failed"; message: string } | { state: "done"; found: CoverSearch };

/** The terms a search for the cover of a book starts from. */
function queryOf(book: Book): CoverQuery {
  return { title: book.title, author: book.authors[0] ?? "", isbn: book.isbn };
}

/** Says which websites a search could not ask, and why each one failed. */
function failures(failed: CoverFailure[]): string {
  return failed.map((failure) => `${failure.source} (${failure.reason})`).join(" and ");
}

/**
 * A dialog for choosing a cover from the internet. It opens on a search
 * for the book as it is filed, whose terms can be changed and searched
 * again, and lists what was found beside the cover the book has now, so
 * that they can be compared. Nothing changes until one is chosen.
 *
 * It has to be rendered only while it is open: it opens when it appears
 * and starts afresh each time.
 */
export function CoverFinder({
  book,
  onClose,
  onChosen,
}: {
  book: Book;
  onClose: () => void;
  onChosen: (book: Book) => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const [form, setForm] = useState<CoverQuery>(() => queryOf(book));
  const [query, setQuery] = useState<CoverQuery>(form);
  const [search, setSearch] = useState<Search>({ state: "searching" });
  const [picked, setPicked] = useState("");
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
  }, []);

  useEffect(() => {
    let current = true;
    setSearch({ state: "searching" });
    setPicked("");
    setProblem("");
    api
      .findCovers(book.id, query)
      .then((found) => current && setSearch({ state: "done", found }))
      .catch((reason) => current && setSearch({ state: "failed", message: messageOf(reason) }));
    return () => {
      current = false;
    };
  }, [book.id, query]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setQuery({ ...form });
  };

  const choose = async () => {
    setSaving(true);
    setProblem("");
    try {
      onChosen(await api.chooseFoundCover(book.id, picked));
    } catch (reason) {
      // The reader cannot see a notice from behind a dialog.
      setProblem(messageOf(reason));
      setSaving(false);
    }
  };

  const found = search.state === "done" ? search.found : undefined;
  const searchable = form.title.trim() !== "" || form.isbn.trim() !== "";

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={onClose}
      onClose={onClose}
      className="m-auto max-h-[calc(100dvh-2rem)] w-[min(56rem,calc(100vw-2rem))] flex-col rounded-sm border border-ink bg-sheet p-0 text-ink open:flex"
    >
      <div className="border-b border-rule px-6 pb-5 pt-6">
        <h2 id={titleId} className="font-display text-[1.35rem] font-medium">
          Find a cover
        </h2>
        <form onSubmit={submit} className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1.4fr)_minmax(0,1fr)_auto]">
          <Field label="Title" className="col-span-2 sm:col-span-1" value={form.title} onChange={(event) => setForm({ ...form, title: event.target.value })} />
          <Field label="Author" value={form.author} onChange={(event) => setForm({ ...form, author: event.target.value })} />
          <Field label="ISBN" value={form.isbn} onChange={(event) => setForm({ ...form, isbn: event.target.value })} />
          <Button type="submit" variant="primary" className="col-span-2 sm:col-span-1 sm:self-end" disabled={!searchable || search.state === "searching"}>
            Search
          </Button>
        </form>
        <p className="mt-2 text-[0.87rem] text-soft">
          Searching sends these terms to Open Library and Google Books. An ISBN finds the exact edition.
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5" aria-live="polite" aria-busy={search.state === "searching"}>
        {search.state === "failed" && <Problem>The search failed: {search.message}</Problem>}
        {search.state === "searching" && <Searching />}
        {found && found.failed.length > 0 && (
          <div className="mb-4">
            <Problem>
              {found.candidates.length > 0
                ? `${failures(found.failed)} failed, so some covers may be missing.`
                : `${failures(found.failed)} failed. Search again in a moment.`}
            </Problem>
          </div>
        )}
        {found && found.candidates.length === 0 && found.failed.length === 0 && (
          <Empty title="No covers found">Try the ISBN, a shorter title or just the author’s surname, then search again.</Empty>
        )}
        {found && found.candidates.length > 0 && (
          <Candidates book={book} candidates={found.candidates} picked={picked} onPick={setPicked} />
        )}
      </div>

      <div className="border-t border-rule px-6 py-4">
        {problem && (
          <div className="mb-3">
            <Problem>{problem}</Problem>
          </div>
        )}
        <div className="flex justify-end gap-3">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" onClick={choose} disabled={picked === "" || saving}>
            {saving ? "Saving…" : "Use this cover"}
          </Button>
        </div>
      </div>
    </dialog>
  );
}

/** What stands in for the covers while they are being searched for. */
function Searching() {
  return (
    <div role="status">
      <p className="mb-4 text-soft">Searching Open Library and Google Books…</p>
      <div className="grid grid-cols-2 gap-x-5 gap-y-6 sm:grid-cols-3 md:grid-cols-4" aria-hidden>
        {[0, 1, 2, 3].map((slot) => (
          <div key={slot}>
            <div className="aspect-[2/3] animate-pulse rounded-l-[2px] rounded-r-[5px] bg-rule/70" />
            <div className="mt-2 h-3.5 w-2/3 animate-pulse rounded-sm bg-rule/70" />
          </div>
        ))}
      </div>
    </div>
  );
}

/**
 * The covers found, one to choose, with the cover the book has first so
 * that they can be compared. Arrow keys move between them as they do
 * between any group of radio buttons.
 */
function Candidates({
  book,
  candidates,
  picked,
  onPick,
}: {
  book: Book;
  candidates: CoverCandidate[];
  picked: string;
  onPick: (id: string) => void;
}) {
  const name = useId();
  return (
    <fieldset>
      <legend className="mb-4 text-soft">
        {candidates.length === 1 ? "One cover found." : `${candidates.length} covers found.`} Choose the one to use.
      </legend>
      <div className="grid grid-cols-2 gap-x-5 gap-y-6 sm:grid-cols-3 md:grid-cols-4">
        {book.cover && (
          <div>
            <Cover book={book} />
            <p className="mt-2 text-[0.87rem] leading-tight">
              <span className="font-medium">Current cover</span>
            </p>
          </div>
        )}
        {candidates.map((candidate) => (
          <label key={candidate.id} className="group relative block cursor-pointer">
            <input
              type="radio"
              name={name}
              className="absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0"
              checked={picked === candidate.id}
              onChange={() => onPick(candidate.id)}
            />
            <span
              className={
                "relative block aspect-[2/3] w-full overflow-hidden rounded-l-[2px] rounded-r-[5px] " +
                "shadow-[0_1px_0_rgb(27_33_30/0.25),0_10px_18px_-10px_rgb(27_33_30/0.55)] transition-shadow " +
                "group-hover:shadow-[0_1px_0_rgb(27_33_30/0.25),0_12px_20px_-8px_rgb(27_33_30/0.7)] " +
                "group-has-[:checked]:outline-[3px] group-has-[:checked]:outline-offset-2 group-has-[:checked]:outline-ribbon " +
                "group-has-[:focus-visible]:outline-[3px] group-has-[:focus-visible]:outline-offset-2 group-has-[:focus-visible]:outline-ribbon"
              }
            >
              <img src={foundCoverUrl(candidate)} alt="" loading="lazy" className="h-full w-full bg-rule/40 object-cover" />
              <span
                aria-hidden
                className="absolute right-1.5 top-1.5 hidden h-6 w-6 place-items-center rounded-full bg-ribbon text-sheet group-has-[:checked]:grid"
              >
                ✓
              </span>
            </span>
            <span className="mt-2 block text-[0.87rem] leading-tight">
              <span className="font-medium">{candidate.source}</span>
              <span className="text-soft">
                {" · "}
                {candidate.width}×{candidate.height}
              </span>
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}
