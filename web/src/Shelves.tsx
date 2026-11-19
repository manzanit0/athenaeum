import { type ReactNode, useId, useState } from "react";
import type { Facets } from "./api";
import { type Branch, branches, leaf, same, separator } from "./categories";
import { languageName } from "./format";
import { Link } from "./router";
import { blank, narrowed, type View } from "./shelf";

/** How many top-level categories are listed before the rest are put behind "Show more". */
const branchesShown = 10;

/** Reports whether list holds value, whatever its case. */
function holds(list: string[], value: string): boolean {
  return list.some((entry) => same(entry, value));
}

/**
 * One shelf: a name and the number of books it leads to. Pressing it
 * shows that shelf, and it stays pressed while it is the one shown. A
 * shelf that leads nowhere stays in its place, dimmed. An open shelf is
 * a category with the chosen one below it.
 */
function Shelf({
  label,
  count,
  chosen,
  open = false,
  below = false,
  onChoose,
}: {
  label: string;
  count?: number;
  chosen: boolean;
  open?: boolean;
  below?: boolean;
  onChoose: () => void;
}) {
  const idle = count === 0 && !chosen;
  const weight = chosen ? "bg-ink/[0.09] font-semibold text-ink" : open ? "font-semibold text-ink" : "text-soft";
  return (
    <button
      type="button"
      aria-pressed={chosen}
      disabled={idle}
      onClick={onChoose}
      className={
        `flex w-full items-baseline gap-3 rounded-sm py-1.5 pr-3 text-left ${below ? "pl-7" : "pl-3"} ` +
        (idle ? "cursor-default text-soft/55" : `hover:bg-ink/5 hover:text-ink ${weight}`)
      }
    >
      <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{label}</span>
      {count !== undefined && <span className="text-[0.87rem] font-normal tabular-nums">{count}</span>}
    </button>
  );
}

/** The shelves of one kind under their name. */
function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <fieldset className="mb-7 min-w-0 last:mb-0">
      <legend className="mb-1 pl-3 font-semibold">{title}</legend>
      <ul>{children}</ul>
    </fieldset>
  );
}

/**
 * The categories of the library as a tree: top-level ones first, each
 * opening to those below it when it, or one below it, is chosen. One
 * category is chosen at a time. Pressing the chosen one again lets go
 * of it: a top-level one gives the whole library back, and one below
 * another gives back the one above it.
 */
function Categories({
  facets,
  library,
  view,
  onShow,
}: {
  facets: Facets;
  library?: Facets;
  view: View;
  onShow: (view: View) => void;
}) {
  const id = useId();
  const [wanted, setWanted] = useState("");
  const [all, setAll] = useState(false);
  const needle = wanted.trim().toLowerCase();
  const tree = branches(facets.tags, library?.tags ?? facets.tags);
  const hit = (name: string) => needle !== "" && name.toLowerCase().includes(needle);
  const within = (branch: Branch) => branch.children.some((child) => holds(view.tags, child.value));
  const picked = (branch: Branch) => holds(view.tags, branch.path) || within(branch);
  const found = (branch: Branch) => hit(branch.path) || branch.children.some((child) => hit(child.value));
  const listed = needle ? tree.filter(found) : tree.filter((branch, index) => all || index < branchesShown || picked(branch));
  const hidden = tree.length - listed.length;
  const choose = (tag: string) => {
    const above = tag.includes(separator) ? [tag.split(separator)[0]] : [];
    onShow({ ...view, untagged: false, tags: holds(view.tags, tag) ? above : [tag] });
  };
  return (
    <Group title="Categories">
      {tree.length > branchesShown && (
        <li className="py-1 pl-3 pr-1">
          <label htmlFor={id} className="sr-only">
            Find a category
          </label>
          <input
            id={id}
            type="search"
            value={wanted}
            onChange={(event) => setWanted(event.target.value)}
            placeholder="Find a category"
            className="w-full rounded-sm border border-rule bg-sheet px-2 py-1 text-[0.93rem] focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
          />
        </li>
      )}
      {facets.tags.length === 0 && (
        <li className="py-1 pl-3 text-[0.9rem] text-soft">
          No book has a category yet. Tick books on the shelf to file many at once.
        </li>
      )}
      {listed.map((branch) => {
        const unfolded = picked(branch) || branch.children.some((child) => hit(child.value));
        const children = needle && !hit(branch.path) ? branch.children.filter((child) => hit(child.value)) : branch.children;
        return (
          <li key={branch.path}>
            <Shelf
              label={branch.path}
              count={branch.count}
              chosen={holds(view.tags, branch.path)}
              open={within(branch)}
              onChoose={() => choose(branch.path)}
            />
            {unfolded && children.length > 0 && (
              <ul className="mb-1">
                {children.map((child) => (
                  <li key={child.value}>
                    <Shelf
                      label={leaf(child.value)}
                      count={child.count}
                      chosen={holds(view.tags, child.value)}
                      below
                      onChoose={() => choose(child.value)}
                    />
                  </li>
                ))}
              </ul>
            )}
          </li>
        );
      })}
      {!needle && tree.length > branchesShown && (hidden > 0 || all) && (
        <li className="pl-3">
          <button
            type="button"
            onClick={() => setAll(!all)}
            className="py-1 text-[0.9rem] underline underline-offset-4 hover:text-ribbon-deep"
          >
            {all ? "Show fewer categories" : `Show ${hidden} more categories`}
          </button>
        </li>
      )}
      {needle !== "" && listed.length === 0 && <li className="py-1 pl-3 text-[0.9rem] text-soft">No category is called that.</li>}
      {((library?.untagged ?? facets.untagged) > 0 || view.untagged) && needle === "" && (
        <li>
          <Shelf
            label="Without a category"
            count={facets.untagged}
            chosen={view.untagged}
            onChoose={() => onShow({ ...view, tags: [], untagged: !view.untagged })}
          />
        </li>
      )}
      {facets.tags.length > 0 && (
        <li className="pl-3 pt-1.5">
          <Link to="/categories" className="text-[0.9rem] text-soft underline underline-offset-4 hover:text-ribbon-deep">
            Manage categories
          </Link>
        </li>
      )}
    </Group>
  );
}

/**
 * The shelves the library can be browsed by: all of it, then its
 * categories, languages and formats, each with the number of books it
 * holds. One shelf of each kind is shown at a time, and shelves of
 * different kinds narrow each other. The lists keep their order while
 * the shelf is narrowed, and a category stands for the categories below
 * it as well.
 */
export function Shelves({
  facets,
  library,
  view,
  onShow,
}: {
  facets: Facets;
  library?: Facets;
  view: View;
  onShow: (view: View) => void;
}) {
  return (
    <>
      <div className="mb-6">
        <Shelf
          label="All books"
          count={facets.total}
          chosen={!narrowed(view)}
          onChoose={() => onShow({ ...blank, query: view.query, sort: view.sort })}
        />
      </div>
      <Categories facets={facets} library={library} view={view} onShow={onShow} />
      {facets.languages.length > 1 && (
        <Group title="Language">
          {facets.languages.map((language) => (
            <li key={language.value}>
              <Shelf
                label={languageName(language.value)}
                count={language.count}
                chosen={holds(view.languages, language.value)}
                onChoose={() =>
                  onShow({ ...view, languages: holds(view.languages, language.value) ? [] : [language.value] })
                }
              />
            </li>
          ))}
        </Group>
      )}
      {facets.formats.length > 1 && (
        <Group title="Format">
          {facets.formats.map((format) => (
            <li key={format.value}>
              <Shelf
                label={format.value.toUpperCase()}
                count={format.count}
                chosen={holds(view.formats, format.value)}
                onChoose={() => onShow({ ...view, formats: holds(view.formats, format.value) ? [] : [format.value] })}
              />
            </li>
          ))}
        </Group>
      )}
    </>
  );
}
