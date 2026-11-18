import { type FormEvent, useState } from "react";
import { api, type Book, type DeviceBook, type DeviceStatus } from "../api";
import { byline, bytes, count } from "../format";
import { useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Link } from "../router";
import { blank } from "../shelf";
import { Button, Confirm, Empty, Field, PageHeader, Problem, Section } from "../ui";

/** The form that sets where the Kindle is mounted. */
function FolderForm({ onSaved }: { onSaved: () => void }) {
  const { notify, attempt } = useNotices();
  const settings = useLoad(api.settings, []);
  const [path, setPath] = useState<string>();
  const save = (event: FormEvent) => {
    event.preventDefault();
    attempt(async () => {
      await api.saveSettings({ kindle_path: path ?? settings.data?.kindle_path ?? "" });
      notify("Kindle folder saved");
      onSaved();
    });
  };
  return (
    <form onSubmit={save} className="flex max-w-xl flex-wrap items-end gap-3">
      <Field
        label="Kindle folder"
        hint="Where the Kindle shows up as a drive, such as /Volumes/Kindle. Leave empty to look in the usual places."
        className="min-w-[16rem] flex-1"
        value={path ?? settings.data?.kindle_path ?? ""}
        onChange={(event) => setPath(event.target.value)}
      />
      <Button type="submit" className="mb-[2.65rem]">
        Save folder
      </Button>
    </form>
  );
}

/** A bar showing how full the Kindle is. */
function Storage({ status }: { status: DeviceStatus }) {
  const used = status.total_bytes - status.free_bytes;
  const share = status.total_bytes > 0 ? (used / status.total_bytes) * 100 : 0;
  return (
    <div className="max-w-xl">
      <div
        role="meter"
        aria-label="Kindle storage used"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(share)}
        className="h-2.5 overflow-hidden rounded-full bg-rule"
      >
        <div className="h-full bg-ink" style={{ width: `${share}%` }} />
      </div>
      <p className="mt-2 text-soft">
        {bytes(status.free_bytes)} free of {bytes(status.total_bytes)}, mounted at {status.path}
      </p>
    </div>
  );
}

/** The table of books stored on the Kindle. */
function OnDevice({ books, onChanged }: { books: DeviceBook[]; onChanged: () => void }) {
  const { notify, attempt } = useNotices();
  const [removing, setRemoving] = useState<DeviceBook>();
  const copy = (book: DeviceBook) =>
    attempt(async () => {
      const added = await api.importFromDevice(book.path);
      notify(`Copied “${added.title}” to the library`);
      onChanged();
    });
  const remove = (book: DeviceBook) =>
    attempt(async () => {
      await api.deleteFromDevice(book.path);
      notify(`Removed “${book.title}” from the Kindle`);
      onChanged();
    });
  if (books.length === 0) {
    return <Empty title="The Kindle has no books">Send one from the list below, or from any book's page.</Empty>;
  }
  return (
    <>
      <ul aria-label="Books on the Kindle" className="divide-y divide-rule border-y border-rule">
        {books.map((book) => (
          <li key={book.path} className="flex flex-wrap items-center gap-x-5 gap-y-1 py-3">
            <div className="min-w-[12rem] flex-1">
              <p className="font-display text-[1.05rem] font-medium leading-snug">{book.title}</p>
              <p className="text-[0.9rem] text-soft">{byline(book.authors)}</p>
            </div>
            <span className="text-soft tabular-nums">
              {book.format.toUpperCase()}, {bytes(book.size)}
            </span>
            {book.book_id ? (
              <Link to={`/books/${book.book_id}`} className="underline underline-offset-4 hover:text-ribbon-deep">
                In library
              </Link>
            ) : (
              <Button variant="quiet" aria-label={`Copy to library: ${book.title}`} onClick={() => copy(book)}>
                Copy to library
              </Button>
            )}
            <Button variant="quiet" aria-label={`Remove from Kindle: ${book.title}`} onClick={() => setRemoving(book)}>
              Remove from Kindle
            </Button>
          </li>
        ))}
      </ul>
      <Confirm
        open={removing !== undefined}
        title={`Remove “${removing?.title}” from the Kindle?`}
        action="Remove from Kindle"
        onCancel={() => setRemoving(undefined)}
        onConfirm={() => {
          if (removing) {
            remove(removing);
          }
          setRemoving(undefined);
        }}
      >
        The file, its notes and its reading position are deleted from the Kindle. The library copy is not touched.
      </Confirm>
    </>
  );
}

/** The library books that can be sent to the Kindle. */
function FromLibrary({ onDevice, onSent }: { onDevice: DeviceBook[]; onSent: () => void }) {
  const { track, attempt } = useNotices();
  const [filter, setFilter] = useState("");
  const library = useLoad(() => api.books(blank), []);
  const present = new Set(onDevice.map((book) => book.book_id));
  const needle = filter.trim().toLowerCase();
  const candidates = (library.data ?? []).filter(
    (book) =>
      !present.has(book.id) &&
      book.formats.length > 0 &&
      `${book.title} ${book.authors.join(" ")}`.toLowerCase().includes(needle),
  );
  const send = (book: Book) =>
    attempt(async () => {
      const job = await api.sendToDevice(book.id);
      track(job, { done: `Sent “${book.title}” to the Kindle`, onDone: onSent });
    });
  return (
    <Section title="Add from the library">
      <Field
        label="Filter library books"
        type="search"
        className="mb-4 max-w-sm"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      {library.data && candidates.length === 0 && (
        <p className="text-soft">
          {needle ? "No library book matches that filter." : "Every library book is already on the Kindle."}
        </p>
      )}
      <ul aria-label="Library books not on the Kindle" className="divide-y divide-rule">
        {candidates.map((book) => (
          <li key={book.id} className="flex flex-wrap items-center gap-x-5 gap-y-1 py-2.5">
            <div className="min-w-[12rem] flex-1">
              <Link to={`/books/${book.id}`} className="font-display font-medium hover:underline">
                {book.title}
              </Link>
              <span className="text-soft"> by {byline(book.authors)}</span>
            </div>
            <span className="text-[0.9rem] text-soft">{book.formats.map((f) => f.format.toUpperCase()).join(", ")}</span>
            <Button aria-label={`Send to Kindle: ${book.title}`} onClick={() => send(book)}>
              Send to Kindle
            </Button>
          </li>
        ))}
      </ul>
    </Section>
  );
}

/** The books on the connected Kindle and the library books to add. */
export function DevicePage() {
  const status = useLoad(api.device, []);
  const books = useLoad(
    () => (status.data?.connected ? api.deviceBooks() : Promise.resolve([])),
    [status.data?.connected, status.data?.path],
  );
  const refresh = () => {
    status.reload();
    books.reload();
  };
  const connected = status.data?.connected ?? false;
  return (
    <>
      <PageHeader
        title="Kindle"
        lead={connected && books.data ? `Connected, ${count(books.data.length, "book")} on the device` : undefined}
      >
        <Button onClick={refresh}>Check again</Button>
      </PageHeader>
      {status.error && <Problem>The Kindle could not be checked: {status.error}</Problem>}
      {status.data && !connected && (
        <div className="space-y-8">
          <Empty title="No Kindle is connected">
            Plug the Kindle in with a USB cable and wait for it to show up as a drive, then choose Check again. When
            Athenaeum runs in Docker, mount that drive into the container.
          </Empty>
          <FolderForm onSaved={refresh} />
        </div>
      )}
      {status.data && connected && (
        <>
          <Section title="Storage">
            <Storage status={status.data} />
          </Section>
          <Section title="On the Kindle">
            {books.error && <Problem>The books on the Kindle could not be listed: {books.error}</Problem>}
            {books.data && <OnDevice books={books.data} onChanged={refresh} />}
          </Section>
          <FromLibrary onDevice={books.data ?? []} onSent={refresh} />
          <Section title="Connection">
            <FolderForm onSaved={refresh} />
          </Section>
        </>
      )}
    </>
  );
}
