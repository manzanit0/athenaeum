import { type ReactNode, useEffect, useId, useState } from "react";
import type { Facets } from "./api";
import { type Branch, branches, leaf, same, separator, withCategory } from "./categories";
import { languageName } from "./format";
import { Link } from "./router";
import type { View } from "./shelf";

const branchesShown = 10;
const searchable = 15;

/** Reports whether list holds value, whatever its case. */
function holds(list: string[], value: string): boolean {
  return list.some((entry) => same(entry, value));
}

/** Returns list with value added when it is missing and removed when it is there. */
function flipped(list: string[], value: string): string[] {
  return holds(list, value) ? list.filter((entry) => !same(entry, value)) : [...list, value];
}

/**
 * The tick box of one value the shelf can be narrowed to, with the
 * number of books it leads to. A value that leads nowhere stays in its
 * place, dimmed. A partial box stands for a category of which only some
 * of the categories below it are ticked.
 */
function Tick({
  label,
  count,
  ticked,
  partial = false,
  onFlip,
}: {
  label: string;
  count?: number;
  ticked: boolean;
  partial?: boolean;
  onFlip: () => void;
}) {
  const idle = count === 0 && !ticked;
  return (
    <label
      className={
        "flex min-w-0 flex-1 items-baseline gap-2.5 py-1 pl-3 pr-1 " +
        (idle ? "text-soft/55" : `cursor-pointer hover:text-ink ${ticked ? "font-semibold text-ink" : "text-soft"}`)
      }
    >
      <input
        type="checkbox"
        checked={ticked}
        disabled={idle}
        onChange={onFlip}
        ref={(box) => {
          if (box) {
            box.indeterminate = partial;
          }
        }}
        className="size-[0.95rem] shrink-0 translate-y-[0.12rem] accent-ribbon"
      />
      <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{label}</span>
      {count !== undefined && <span className="text-[0.87rem] font-normal tabular-nums">{count}</span>}
    </label>
  );
}

/** The choices of one kind, led by the one that lifts the narrowing by that kind. */
function Group({
  title,
  any,
  free,
  onFree,
  children,
}: {
  title: string;
  any: string;
  free: boolean;
  onFree: () => void;
  children: ReactNode;
}) {
  return (
    <fieldset className="mb-7 min-w-0">
      <legend className="mb-1.5 pl-3 font-semibold">{title}</legend>
      <ul>
        <li className="flex">
          <Tick label={any} ticked={free} onFlip={onFree} />
        </li>
        {children}
      </ul>
    </fieldset>
  );
}

/**
 * The categories of the library as a tree: top-level ones first, each
 * opening to those below it. Ticking a category opens it, so the ones
 * below it are at hand to narrow the shelf further, and it stays open
 * when unticked. A category and the ones below it are never ticked
 * together: ticking a category shows all of it, and ticking one below
 * it narrows the shelf to that one.
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
  const [open, setOpen] = useState<string[]>([]);
  const [all, setAll] = useState(false);
  const needle = wanted.trim().toLowerCase();
  const ticked = view.tags.join("\n");
  useEffect(() => {
    const tops = view.tags.map((tag) => tag.split(separator)[0]);
    setOpen((before) => [...before, ...tops.filter((top, index) => !holds(before, top) && tops.indexOf(top) === index)]);
  }, [ticked]);
  const tree = branches(facets.tags, library?.tags ?? facets.tags);
  const hit = (name: string) => needle !== "" && name.toLowerCase().includes(needle);
  const picked = (branch: Branch) =>
    holds(view.tags, branch.path) || branch.children.some((child) => holds(view.tags, child.value));
  const found = (branch: Branch) => hit(branch.path) || branch.children.some((child) => hit(child.value));
  const listed = needle ? tree.filter(found) : tree.filter((branch, index) => all || index < branchesShown || picked(branch));
  const hidden = tree.length - listed.length;
  const flip = (tag: string) =>
    onShow({ ...view, tags: holds(view.tags, tag) ? flipped(view.tags, tag) : withCategory(view.tags, tag) });
  return (
    <Group
      title="Categories"
      any="Any category"
      free={view.tags.length === 0 && !view.untagged}
      onFree={() => onShow({ ...view, tags: [], untagged: false })}
    >
      {facets.tags.length > searchable && (
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
          No book has a category yet. Categorise books files many at once.
        </li>
      )}
      {listed.map((branch) => {
        const unfolded = holds(open, branch.path) || branch.children.some((child) => hit(child.value));
        const children = needle && !hit(branch.path) ? branch.children.filter((child) => hit(child.value)) : branch.children;
        return (
          <li key={branch.path}>
            <div className="flex items-center">
              <Tick
                label={branch.path}
                count={branch.count}
                ticked={holds(view.tags, branch.path)}
                partial={branch.children.some((child) => holds(view.tags, child.value))}
                onFlip={() => flip(branch.path)}
              />
              {branch.children.length > 0 ? (
                <button
                  type="button"
                  aria-expanded={unfolded}
                  aria-label={`${unfolded ? "Hide" : "Show"} the categories under ${branch.path}`}
                  onClick={() => setOpen(flipped(open, branch.path))}
                  className="grid size-7 shrink-0 place-items-center rounded-sm text-soft hover:bg-ink/5 hover:text-ink"
                >
                  <svg
                    aria-hidden
                    viewBox="0 0 12 12"
                    className={`size-3 transition-transform duration-150 ${unfolded ? "rotate-90" : ""}`}
                  >
                    <path d="M4 2l4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </button>
              ) : (
                <span className="size-7 shrink-0" />
              )}
            </div>
            {unfolded && (
              <ul className="mb-1 ml-[1.15rem] border-l border-rule pr-7">
                {children.map((child) => (
                  <li key={child.value} className="flex">
                    <Tick
                      label={leaf(child.value)}
                      count={child.count}
                      ticked={holds(view.tags, child.value)}
                      onFlip={() => flip(child.value)}
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
        <li className="flex pr-7">
          <Tick
            label="Without a category"
            count={facets.untagged}
            ticked={view.untagged}
            onFlip={() => onShow({ ...view, untagged: !view.untagged })}
          />
        </li>
      )}
      {facets.tags.length > 0 && (
        <li className="pl-3 pt-1.5">
          <Link to="/categories" className="text-[0.9rem] underline underline-offset-4 hover:text-ribbon-deep">
            Manage categories
          </Link>
        </li>
      )}
    </Group>
  );
}

/**
 * The choices that narrow the shelf: categories, languages and formats.
 * The lists keep their order while the shelf is narrowed. Ticking several
 * values of one kind shows the books that have any of them, and a
 * category stands for the categories below it as well.
 */
export function Narrow({
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
      <Categories facets={facets} library={library} view={view} onShow={onShow} />
      {facets.languages.length > 1 && (
        <Group
          title="Language"
          any="Any language"
          free={view.languages.length === 0}
          onFree={() => onShow({ ...view, languages: [] })}
        >
          {facets.languages.map((language) => (
            <li key={language.value} className="flex">
              <Tick
                label={languageName(language.value)}
                count={language.count}
                ticked={holds(view.languages, language.value)}
                onFlip={() => onShow({ ...view, languages: flipped(view.languages, language.value) })}
              />
            </li>
          ))}
        </Group>
      )}
      {facets.formats.length > 1 && (
        <Group
          title="Format"
          any="Any format"
          free={view.formats.length === 0}
          onFree={() => onShow({ ...view, formats: [] })}
        >
          {facets.formats.map((format) => (
            <li key={format.value} className="flex">
              <Tick
                label={format.value.toUpperCase()}
                count={format.count}
                ticked={holds(view.formats, format.value)}
                onFlip={() => onShow({ ...view, formats: flipped(view.formats, format.value) })}
              />
            </li>
          ))}
        </Group>
      )}
    </>
  );
}
