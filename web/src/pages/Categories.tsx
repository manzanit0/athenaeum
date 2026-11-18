import { type FormEvent, useState } from "react";
import { api, type Tally } from "../api";
import { branches, leaf, tidy } from "../categories";
import { count } from "../format";
import { useLoad } from "../hooks";
import { useNotices } from "../notices";
import { Link } from "../router";
import { blank, shelfHref, viewHref } from "../shelf";
import { Button, Confirm, Empty, PageHeader, Problem } from "../ui";

/** One category with the ways to rename it, merge it into another or remove it. */
function Row({
  category,
  nested,
  onRename,
  onRemove,
}: {
  category: Tally;
  nested: boolean;
  onRename: (to: string) => Promise<boolean>;
  onRemove: () => void;
}) {
  const [name, setName] = useState<string>();
  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (name !== undefined && tidy(name) && (await onRename(tidy(name)))) {
      setName(undefined);
    }
  };
  return (
    <li className={`flex flex-wrap items-center gap-x-5 gap-y-2 py-2.5 ${nested ? "pl-7" : ""}`}>
      {name === undefined ? (
        <>
          <Link
            to={viewHref({ ...blank, tags: [category.value] })}
            className={`min-w-0 flex-1 underline-offset-4 hover:underline ${nested ? "" : "font-semibold"}`}
          >
            {nested ? leaf(category.value) : category.value}
          </Link>
          <span className="text-soft tabular-nums">{count(category.count, "book")}</span>
          <Button variant="quiet" aria-label={`Rename ${category.value}`} onClick={() => setName(category.value)}>
            Rename
          </Button>
          <Button variant="quiet" aria-label={`Remove ${category.value}`} onClick={onRemove}>
            Remove
          </Button>
        </>
      ) : (
        <form onSubmit={save} className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
          <input
            autoFocus
            aria-label={`New name for ${category.value}`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            className="min-w-[12rem] flex-1 rounded-sm border border-rule bg-sheet px-3 py-1.5 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
          />
          <Button type="submit" variant="primary" disabled={!tidy(name) || tidy(name) === category.value}>
            Save name
          </Button>
          <Button onClick={() => setName(undefined)}>Cancel</Button>
        </form>
      )}
    </li>
  );
}

/** The categories of the library, to rename, merge and remove. */
export function CategoriesPage() {
  const { notify, attempt } = useNotices();
  const library = useLoad(() => api.facets(blank), []);
  const [doomed, setDoomed] = useState<Tally>();
  const tags = library.data?.tags ?? [];
  const tree = branches(tags, tags);

  const rename = (from: string, to: string) =>
    attempt(async () => {
      const merged = tags.some((entry) => entry.value.toLowerCase() === to.toLowerCase());
      const result = await api.renameTag(from, to);
      notify(`${merged ? "Merged" : "Renamed"} “${from}” ${merged ? "into" : "to"} “${to}” on ${count(result.books, "book")}`);
      library.reload();
    });
  const remove = (category: Tally) =>
    attempt(async () => {
      const result = await api.renameTag(category.value, "");
      notify(`Took ${count(result.books, "book")} out of “${category.value}”`);
      library.reload();
    });

  return (
    <>
      <Link to={shelfHref()} className="text-soft underline underline-offset-4 hover:text-ink">
        Back to the library
      </Link>
      <div className="mt-3">
        <PageHeader
          title="Categories"
          lead="Books are browsed by these. Giving a category the name of another merges the two; write one below another as Parent / Name."
        />
      </div>
      {library.error && <Problem>The categories could not be loaded: {library.error}</Problem>}
      {library.data && tree.length === 0 && (
        <Empty title="No categories yet">
          Open a book to file it under one, or choose <strong className="font-semibold text-ink">Categorise books</strong>{" "}
          in the library to file many at once.
        </Empty>
      )}
      <ul className="max-w-[46rem] divide-y divide-rule border-y border-rule empty:hidden">
        {tree.flatMap((branch) => [
          <Row
            key={branch.path}
            category={{ value: branch.path, count: branch.count }}
            nested={false}
            onRename={(to) => rename(branch.path, to)}
            onRemove={() => setDoomed({ value: branch.path, count: branch.count })}
          />,
          ...branch.children.map((child) => (
            <Row
              key={child.value}
              category={child}
              nested
              onRename={(to) => rename(child.value, to)}
              onRemove={() => setDoomed(child)}
            />
          )),
        ])}
      </ul>
      <Confirm
        open={doomed !== undefined}
        title={`Remove “${doomed?.value ?? ""}”?`}
        action="Remove category"
        onCancel={() => setDoomed(undefined)}
        onConfirm={() => {
          if (doomed) {
            remove(doomed);
          }
          setDoomed(undefined);
        }}
      >
        {count(doomed?.count ?? 0, "book")} will no longer be filed under it or under the categories below it. The
        books stay in the library.
      </Confirm>
    </>
  );
}
