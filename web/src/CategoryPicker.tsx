import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react";
import type { Tally } from "./api";
import { same, separator, tidy, under, withCategory } from "./categories";
import { count } from "./format";

/** The most categories a book is filed under. */
export const mostCategories = 3;

/** How many of a book's categories are listed before the rest are put behind "Show all". */
const listedAtFirst = 12;

/** The look of the small actions under the categories. */
const quiet = "rounded-sm font-medium underline underline-offset-4 hover:text-ribbon-deep";

/** A category offered for adding, with what is said beside it. */
interface Offer {
  value: string;
  note: string;
  /** Whether no book of the library is filed under it yet. */
  fresh: boolean;
}

/** What the field offers for what is typed, or why it offers nothing. */
interface Offers {
  list: Offer[];
  /** How many more would be offered if the list were not cut short. */
  more: number;
  nothing: string;
}

/** The most categories the field offers at once; typing narrows them down. */
const mostOffered = 40;

/** Reports whether the category, or a level of it, starts with the typed text. */
function starts(tag: string, typed: string): boolean {
  const lower = tag.toLowerCase();
  return lower.startsWith(typed) || lower.split(separator).some((level) => level.startsWith(typed));
}

/**
 * Works out what to offer for the typed text: the library's categories
 * that contain it, the closest first, and the text itself as a new
 * category when the library has none of that name. Without text every
 * category is offered, those just taken off the book first. A category
 * that would put the book over its limit is not offered.
 */
function offersFor(typed: string, value: string[], known: Tally[], removed: string[]): Offers {
  const name = tidy(typed);
  const needle = name.toLowerCase();
  const held = (tag: string) => value.some((entry) => same(entry, tag));
  const gone = new Set(removed.map((tag) => tag.toLowerCase()));
  const few = removed.length <= listedAtFirst;
  // What was only on this book and has been taken off it. After a clear-out
  // of many, these go last, so the categories the library uses come first.
  const dropped: Offer[] = [];
  const all: Offer[] = [];
  for (const tag of removed) {
    if (few || !known.some((entry) => same(entry.value, tag) && entry.count > 1)) {
      (few ? all : dropped).push({ value: tag, note: "just removed", fresh: false });
    }
  }
  const listedAlready = new Set([...all, ...dropped].map((offer) => offer.value.toLowerCase()));
  for (const entry of known) {
    if (!listedAlready.has(entry.value.toLowerCase())) {
      const note = gone.has(entry.value.toLowerCase()) ? "just removed" : count(entry.count, "book");
      all.push({ value: entry.value, note, fresh: false });
    }
  }
  all.push(...dropped);
  const rank = (offer: Offer) =>
    offer.fresh ? 2 : same(offer.value, name) ? 0 : starts(offer.value, needle) ? 1 : 3;
  const matching = all.filter((offer) => !held(offer.value) && offer.value.toLowerCase().includes(needle));
  if (name && !held(name) && !all.some((offer) => same(offer.value, name))) {
    // A new category below one the library has is written the way the library writes that one.
    const above = all
      .map((offer) => offer.value)
      .filter((tag) => under(name, tag))
      .sort((a, b) => b.length - a.length)[0];
    matching.push({ value: above ? above + name.slice(above.length) : name, note: "new category", fresh: true });
  }
  const fitting = matching
    .filter((offer) => withCategory(value, offer.value).length <= mostCategories)
    .sort((a, b) => (name ? rank(a) - rank(b) : 0));
  const list = fitting.slice(0, mostOffered);
  const more = fitting.length - list.length;
  if (list.length > 0) {
    return { list, more, nothing: "" };
  }
  if (name && held(name)) {
    return { list, more, nothing: `This book is already filed under “${name}”.` };
  }
  if (matching.length > 0) {
    return { list, more, nothing: "A book takes up to three categories. Remove one to add another." };
  }
  return { list, more, nothing: "The library has no other categories. Type a name to make one." };
}

/**
 * The categories of a book: those it is filed under, each removable and
 * all removable at once, and one field that adds a category, whether the
 * library has it already or not. Typing in the field narrows the
 * library's categories down. Adding a category replaces a less specific
 * one of its branch, and what was last removed can be put back.
 */
export function CategoryPicker({
  value,
  known,
  onChange,
}: {
  value: string[];
  known: Tally[];
  onChange: (next: string[]) => void;
}) {
  const id = useId();
  const field = useRef<HTMLInputElement>(null);
  const chips = useRef<HTMLUListElement>(null);
  const choices = useRef<HTMLUListElement>(null);
  const [typed, setTyped] = useState("");
  const [shown, setShown] = useState(false);
  const [active, setActive] = useState(-1);
  const [removed, setRemoved] = useState<string[]>([]);
  const [undo, setUndo] = useState<{ list: string[]; note: string }>();
  const offers = offersFor(typed, value, known, removed);
  const over = value.length > mostCategories;
  const [unfolded, setUnfolded] = useState(false);
  const many = value.length > listedAtFirst;
  const listed = many && !unfolded ? value.slice(0, listedAtFirst) : value;

  useEffect(() => {
    choices.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const type = (text: string) => {
    setTyped(text);
    setShown(true);
    setActive(tidy(text) ? 0 : -1);
  };
  const add = (tag: string) => {
    onChange(withCategory(value, tag));
    setUndo(undefined);
    setTyped("");
    setShown(false);
    setActive(-1);
  };
  const take = (tags: string[], note: string) => {
    setUndo({ list: value, note });
    setRemoved([...tags, ...removed.filter((entry) => !tags.includes(entry))]);
    onChange(value.filter((entry) => !tags.includes(entry)));
  };
  /** Removes one category and keeps the keyboard where it was: on the next one, or on the field after the last. */
  const remove = (tag: string) => {
    const buttons = Array.from(chips.current?.querySelectorAll("button") ?? []);
    const at = value.indexOf(tag);
    (buttons[at + 1] ?? buttons[at - 1] ?? field.current)?.focus();
    take([tag], `Removed “${tag}”.`);
  };
  const clear = () => {
    take(value, `Removed all ${value.length} categories.`);
    field.current?.focus();
  };
  const restore = () => {
    if (undo) {
      onChange(undo.list);
      setUndo(undefined);
      field.current?.focus();
    }
  };
  const keys = (event: KeyboardEvent<HTMLInputElement>) => {
    const last = offers.list.length - 1;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const step = event.key === "ArrowDown" ? 1 : -1;
      setActive(!shown ? Math.max(active, 0) : active + step > last ? 0 : active + step < 0 ? last : active + step);
      setShown(true);
    } else if (event.key === "Enter") {
      // Enter adds a category and never saves the whole form from here.
      event.preventDefault();
      if (shown && offers.list[active]) {
        add(offers.list[active].value);
      }
    } else if (event.key === "Escape" && shown) {
      event.stopPropagation();
      setShown(false);
    }
  };

  // What can be done to the list as a whole. It sits under a short list and above a long one, where it would otherwise be pages away.
  const actions = (
    <div className={`flex flex-wrap items-baseline gap-x-4 text-[0.93rem] ${value.length > 1 || undo ? (many ? "mb-2" : "mt-2") : ""}`}>
      <span role="status" className="text-soft empty:hidden">
        {undo?.note}
      </span>
      {undo && (
        <button type="button" className={quiet} onClick={restore}>
          Undo
        </button>
      )}
      {value.length > 1 && (
        <button type="button" className={quiet} onClick={clear}>
          Remove all
        </button>
      )}
      {many && (
        <button type="button" className={quiet} aria-expanded={unfolded} onClick={() => setUnfolded(!unfolded)}>
          {unfolded ? `Show the first ${listedAtFirst} only` : `Show all ${value.length}`}
        </button>
      )}
    </div>
  );

  return (
    <fieldset className="min-w-0 sm:col-span-6">
      <legend className="mb-1 font-medium">Categories</legend>
      {value.length === 0 && !undo && <p className="text-soft">This book is not filed under a category yet.</p>}
      {over && (
        <p className="mb-2 max-w-[62ch] text-soft">
          This book has {value.length} categories, and a book takes up to three. Remove the ones that do not fit, or
          remove them all and start again.
        </p>
      )}
      {many && actions}
      <ul ref={chips} className="flex flex-wrap gap-2 empty:hidden">
        {listed.map((tag) => (
          <li key={tag} className="inline-flex items-center gap-2 rounded-sm border border-ink py-1 pl-2.5 pr-1 leading-tight">
            {tag}
            <button
              type="button"
              aria-label={`Remove ${tag}`}
              onClick={() => remove(tag)}
              className="grid size-5 place-items-center rounded-sm text-soft hover:bg-ink/10 hover:text-ink"
            >
              <span aria-hidden>×</span>
            </button>
          </li>
        ))}
      </ul>
      {!many && actions}
      <div className="relative mt-3 max-w-[26rem]">
        <label htmlFor={`${id}-field`} className="mb-1 block text-[0.93rem] text-soft">
          Add a category
        </label>
        <input
          id={`${id}-field`}
          ref={field}
          role="combobox"
          aria-expanded={shown}
          aria-controls={`${id}-choices`}
          aria-autocomplete="list"
          aria-activedescendant={shown && offers.list[active] ? `${id}-choice-${active}` : undefined}
          aria-describedby={`${id}-hint`}
          autoComplete="off"
          value={typed}
          placeholder="History / Military"
          onChange={(event) => type(event.target.value)}
          onClick={() => setShown(true)}
          onBlur={() => setShown(false)}
          onKeyDown={keys}
          className="w-full rounded-sm border border-rule bg-sheet px-3 py-2 placeholder:text-soft/70 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
        />
        <ul
          id={`${id}-choices`}
          ref={choices}
          role="listbox"
          aria-label="Categories to add"
          hidden={!shown}
          // Keeps the field focused while a choice is pressed, so the press lands before the list closes.
          onMouseDown={(event) => event.preventDefault()}
          className="absolute inset-x-0 top-full z-20 mt-1 max-h-[17rem] overflow-y-auto rounded-sm border border-ink bg-sheet py-1 shadow-[0_10px_24px_-12px_rgb(27_33_30/0.6)]"
        >
          {offers.list.map((offer, index) => (
            <li
              key={offer.value}
              id={`${id}-choice-${index}`}
              role="option"
              aria-selected={index === active}
              onClick={() => add(offer.value)}
              onMouseMove={() => setActive(index)}
              className={`flex cursor-pointer items-baseline justify-between gap-4 px-3 py-1.5 ${index === active ? "bg-ink/10" : ""}`}
            >
              <span className="min-w-0">{offer.fresh ? `Add “${offer.value}”` : offer.value}</span>
              <span className="shrink-0 text-[0.87rem] text-soft">{offer.note}</span>
            </li>
          ))}
          {offers.more > 0 && (
            <li role="presentation" className="px-3 py-1.5 text-[0.87rem] text-soft">
              And {offers.more} more. Type to narrow them down.
            </li>
          )}
          {offers.list.length === 0 && (
            <li role="presentation" className="px-3 py-1.5 text-soft">
              {offers.nothing}
            </li>
          )}
        </ul>
      </div>
      <p id={`${id}-hint`} className="mt-2 max-w-[62ch] text-[0.87rem] text-soft">
        A book takes up to three categories, the most specific ones. Write a category below another as Parent / Name.
      </p>
    </fieldset>
  );
}
