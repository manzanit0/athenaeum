import { useState } from "react";
import type { BookChange, FileChange, SyncSummary } from "./api";
import { bytes, count } from "./format";
import { Link } from "./router";

type Kind = BookChange["kind"];

const kinds: Kind[] = ["add", "update", "remove"];

const wording: Record<Kind, { mark: string; tone: string; planned: string; done: string; heading: [string, string] }> = {
  add: { mark: "+", tone: "text-leaf", planned: "to add", done: "added", heading: ["Add", "Added"] },
  update: { mark: "~", tone: "text-soft", planned: "to update", done: "updated", heading: ["Update", "Updated"] },
  remove: { mark: "−", tone: "text-ribbon-deep", planned: "to remove", done: "removed", heading: ["Remove", "Removed"] },
};

const fileVerbs: Record<FileChange["op"], string> = {
  add: "added",
  replace: "replaced",
  remove: "removed",
  rename: "renamed",
};

const otherVerbs: Record<FileChange["op"], [string, string]> = {
  add: ["Add", "Added"],
  replace: ["Replace", "Replaced"],
  remove: ["Remove", "Removed"],
  rename: ["Rename", "Renamed"],
};

const shown = 8;

/** Names the format of a file, such as "EPUB". */
function formatOf(path: string): string {
  return path.slice(path.lastIndexOf(".") + 1).toUpperCase();
}

/** Says in a few words what changes about a book. */
function about(change: BookChange): string {
  if (change.kind === "remove") {
    return "";
  }
  if (change.kind === "add") {
    const formats = change.files.map((file) => formatOf(file.path));
    const loose = !change.details && !change.book_id ? ["a file to be filed as a book"] : [];
    return [...formats, ...(change.bytes > 0 ? [bytes(change.bytes)] : []), ...loose].join(", ");
  }
  const parts = [
    ...(change.details ? ["details changed"] : []),
    ...(change.cover ? [`cover ${fileVerbs[change.cover]}`] : []),
    ...change.files.map((file) => `${formatOf(file.path)} ${fileVerbs[file.op]}`),
  ];
  const text = parts.join(", ");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** Counts the books of a side by what happens to them, in words. */
export function tally(summary: SyncSummary, planned: boolean): string {
  const parts = kinds
    .map((kind) => ({ kind, total: summary.books.filter((book) => book.kind === kind).length }))
    .filter(({ total }) => total > 0)
    .map(({ kind, total }) => `${total} ${planned ? wording[kind].planned : wording[kind].done}`);
  const files = summary.other.length > 0 ? [count(summary.other.length, "other file")] : [];
  if (parts.length === 0) {
    return files.join("");
  }
  return [`${count(summary.books.length, "book")}: ${parts.join(", ")}`, ...files].join(", and ");
}

/** Reports whether a sync changes nothing on a side. */
export function unchanged(summary?: SyncSummary): boolean {
  return !summary || (summary.books.length === 0 && summary.other.length === 0);
}

/** One book of a group: what it is and what changes about it. */
function Row({ change }: { change: BookChange }) {
  const { mark, tone } = wording[change.kind];
  const detail = about(change);
  const renames = change.files.filter((file) => file.op === "rename");
  return (
    <li className="flex gap-3 py-1.5">
      <span aria-hidden className={`w-3 shrink-0 text-center font-semibold ${tone}`}>
        {mark}
      </span>
      <div className="min-w-0 flex-1">
        <p>
          {change.book_id && change.kind !== "remove" ? (
            <Link to={`/books/${change.book_id}`} className="font-medium hover:underline">
              {change.title}
            </Link>
          ) : (
            <span className="font-medium">{change.title}</span>
          )}
          {change.authors.length > 0 && <span className="text-soft"> by {change.authors.join(", ")}</span>}
        </p>
        {detail && <p className="text-[0.9rem] text-soft">{detail}</p>}
        {renames.map((file) => (
          <p key={file.path} className="break-words text-[0.9rem] text-soft">
            {file.from} is now {file.path}
          </p>
        ))}
      </div>
    </li>
  );
}

/** The books of a side that share a fate, a few at first. */
function Group({ kind, books, planned, note }: { kind: Kind; books: BookChange[]; planned: boolean; note?: string }) {
  const [all, setAll] = useState(false);
  if (books.length === 0) {
    return null;
  }
  const heading = wording[kind].heading[planned ? 0 : 1];
  return (
    <div role="group" aria-label={heading} className="mt-3">
      <p className={`text-[0.9rem] font-semibold ${wording[kind].tone}`}>
        {heading} <span className="font-normal text-soft">{books.length}</span>
      </p>
      <ul>
        {(all ? books : books.slice(0, shown)).map((change) => (
          <Row key={`${change.title}:${change.files[0]?.path ?? ""}`} change={change} />
        ))}
      </ul>
      {books.length > shown && (
        <button type="button" className="ml-6 text-[0.9rem] underline underline-offset-4" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : `Show all ${books.length}`}
        </button>
      )}
      {note && <p className="ml-6 mt-1 text-[0.9rem] text-soft">{note}</p>}
    </div>
  );
}

/** The files of a side that belong to no book, out of the way. */
function Others({ files, planned }: { files: FileChange[]; planned: boolean }) {
  if (files.length === 0) {
    return null;
  }
  return (
    <details className="mt-3">
      <summary className="cursor-pointer text-[0.9rem] underline underline-offset-4">
        {count(files.length, "other file")}, not part of a book
      </summary>
      <ul className="mt-2 max-h-72 overflow-auto border-l-2 border-rule pl-4 text-[0.9rem]">
        {files.map((file) => (
          <li key={`${file.op}:${file.path}`} className="break-all py-0.5">
            <span className="text-soft">{otherVerbs[file.op][planned ? 0 : 1]}</span> {file.path}
          </li>
        ))}
      </ul>
    </details>
  );
}

/**
 * What a sync changes on one side, book by book: a count in words, then
 * the books to add, to update and to remove, each named with what
 * changes about it.
 */
export function Side({
  title,
  summary,
  planned,
  transfer,
  removalNote,
}: {
  title: string;
  summary: SyncSummary;
  planned: boolean;
  transfer?: string;
  removalNote: string;
}) {
  const nothing = unchanged(summary);
  return (
    <div role="group" aria-label={title} className="mt-4 border-t border-rule pt-3 first:mt-3">
      <p>
        <span className="font-medium">{title}</span>
        <span className="text-soft">
          {" "}
          {nothing ? (planned ? "nothing to change" : "nothing changed") : tally(summary, planned)}
          {!nothing && transfer && `, ${transfer}`}
        </span>
      </p>
      {kinds.map((kind) => (
        <Group
          key={kind}
          kind={kind}
          planned={planned}
          books={summary.books.filter((book) => book.kind === kind)}
          note={kind === "remove" ? removalNote : undefined}
        />
      ))}
      <Others files={summary.other} planned={planned} />
    </div>
  );
}
