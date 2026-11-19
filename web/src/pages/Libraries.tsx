import { type FormEvent, useId, useState } from "react";
import { api, type FolderCheck, type Libraries, type LibraryEntry } from "../api";
import { messageOf, sentence } from "../format";
import { type Loaded, useDebounced, useLoad } from "../hooks";
import { useNotices } from "../notices";
import { type Opening, useOpener } from "../opening";
import { Button, Field, PageHeader, Problem, Section } from "../ui";

/** What the page says about a typed folder, and what its button then does. */
const verdicts: Record<FolderCheck["kind"], { says: string; action: string }> = {
  library: { says: "This folder holds a library.", action: "Open library" },
  empty: { says: "This folder is empty. A new library starts here.", action: "Start a library here" },
  new: {
    says: "This folder does not exist yet. It is created, and a new library starts in it.",
    action: "Create folder and start a library",
  },
  other: {
    says:
      "This folder holds other files and no library. A new library starts beside them. " +
      "The files are left alone; to add them as books, import them afterwards.",
    action: "Start a library beside these files",
  },
  invalid: { says: "", action: "Open library" },
};

/** Rewrites the folders the server suggests the way the path was typed, so a tilde stays a tilde. */
function asTyped(typed: string, folders: string[]): string[] {
  const stem = typed.slice(0, typed.lastIndexOf("/") + 1);
  return folders.map((folder) => `${stem}${folder.slice(folder.lastIndexOf("/") + 1)}/`);
}

/** Says why no library is open and offers to try the same one again. */
function Trouble({ libraries, onRetry }: { libraries: Libraries; onRetry: (path: string) => void }) {
  const problem = libraries.problem;
  if (!problem) {
    return <Problem>No library is open. Add one, or open one of your libraries below.</Problem>;
  }
  const name = problem.path.slice(problem.path.lastIndexOf("/") + 1);
  return (
    <div role="alert" className="rounded-sm border-l-4 border-ribbon bg-sheet px-4 py-3">
      <p className="font-medium">
        The library “{name}” {problem.missing ? "cannot be found" : "could not be opened"}
      </p>
      <p className="mt-1 break-words text-soft">
        {problem.missing
          ? `Its folder, ${problem.path}, is missing. If it is on an external drive, connect the drive and try again.`
          : `${sentence(problem.detail)} Its folder is ${problem.path}.`}
      </p>
      <Button className="mt-3" onClick={() => onRetry(problem.path)}>
        Try again
      </Button>
    </div>
  );
}

/** One known library: its name and folder, whether it is open, and what can be done with it. */
function Row({
  library,
  onOpen,
  onForget,
}: {
  library: LibraryEntry;
  onOpen: () => void;
  onForget: () => void;
}) {
  return (
    <li className="flex flex-wrap items-center gap-x-5 gap-y-2 py-3">
      <div className="min-w-0 flex-1">
        <p className="font-medium">
          {library.name}
          {library.state === "open" && <span className="ml-2 text-[0.87rem] font-normal text-leaf">Open</span>}
          {library.state === "missing" && (
            <span className="ml-2 text-[0.87rem] font-normal text-ribbon-deep">Folder not found</span>
          )}
        </p>
        <p className="break-all text-[0.9rem] text-soft">{library.path}</p>
      </div>
      {library.state !== "open" && (
        <>
          <Button variant="quiet" aria-label={`Remove ${library.name} from the list`} onClick={onForget}>
            Remove from list
          </Button>
          <Button aria-label={`Open ${library.name}`} disabled={library.state === "missing"} onClick={onOpen}>
            Open
          </Button>
        </>
      )}
    </li>
  );
}

/**
 * The form that opens the library in a typed folder. The server says
 * what is at the path as it is typed and suggests the folders that
 * could come next, since a page cannot browse the computer the server
 * runs on.
 */
function FolderForm({ onOpen }: { onOpen: (opening: Opening) => void }) {
  const listId = useId();
  const [path, setPath] = useState("");
  const typed = useDebounced(path.trim(), 200);
  const check = useLoad(() => (typed ? api.checkFolder(typed) : Promise.resolve(undefined)), [typed]);
  const folders = useLoad(() => (typed ? api.suggestFolders(typed) : Promise.resolve([])), [typed]);
  const found = typed === path.trim() ? check.data : undefined;
  const usable = found !== undefined && found.kind !== "invalid";
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (found && usable) {
      onOpen({ path: found.path, create: found.kind !== "library" });
    }
  };
  return (
    <form onSubmit={submit} className="max-w-2xl">
      <Field
        label="Folder"
        hint="The whole path of a folder on the computer Athenaeum runs on, such as ~/Books."
        list={listId}
        autoComplete="off"
        spellCheck={false}
        value={path}
        onChange={(event) => setPath(event.target.value)}
      />
      <datalist id={listId}>
        {asTyped(path.trim(), folders.data ?? []).map((folder) => (
          <option key={folder} value={folder} />
        ))}
      </datalist>
      <p role="status" className={`mt-2 min-h-6 ${found?.kind === "invalid" ? "text-ribbon-deep" : ""}`}>
        {found && (found.kind === "invalid" ? sentence(found.detail ?? "") : verdicts[found.kind].says)}
      </p>
      <Button type="submit" variant="primary" className="mt-2" disabled={!usable}>
        {verdicts[found?.kind ?? "invalid"].action}
      </Button>
    </form>
  );
}

/**
 * The page that adds and removes libraries. A library is a folder; one
 * is open at a time, and switching between the known ones is done from
 * the control beside the application's name. With none open, which is
 * how the application starts when a library's folder has gone missing,
 * this page is all there is until one is opened.
 */
export function LibrariesPage({ libraries }: { libraries: Loaded<Libraries> }) {
  const { notify } = useNotices();
  const { open, dialog } = useOpener(libraries.reload);
  const data = libraries.data;
  const forget = async (library: LibraryEntry) => {
    try {
      libraries.set(await api.forgetLibrary(library.path));
      notify(`Removed “${library.name}” from the list. Its folder was left alone.`);
    } catch (reason) {
      notify(sentence(messageOf(reason)), "error");
    }
  };
  if (!data) {
    return libraries.error ? <Problem>{libraries.error}</Problem> : null;
  }
  return (
    <>
      <PageHeader
        title="Libraries"
        lead="A library is a folder with its own books, catalogue and MEGA sign-in. One is open at a time."
      />
      {!data.current && (
        <div className="mb-10">
          <Trouble libraries={data} onRetry={(path) => open({ path, create: false })} />
        </div>
      )}
      <Section title="Add a library">
        <FolderForm onOpen={open} />
      </Section>
      <Section title="Your libraries">
        <ul className="divide-y divide-rule border-y border-rule">
          {data.libraries.map((library) => (
            <Row
              key={library.path}
              library={library}
              onOpen={() => open({ path: library.path, create: false })}
              onForget={() => forget(library)}
            />
          ))}
        </ul>
        {data.libraries.length === 0 && <p className="text-soft">No library is known yet.</p>}
      </Section>
      {dialog}
    </>
  );
}
