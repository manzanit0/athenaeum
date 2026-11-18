import { type Book, coverUrl } from "./api";
import { byline } from "./format";

const cloths = ["#2f4b3c", "#6b2737", "#25344f", "#7b5a16", "#454a54", "#733c1e", "#3d3a5c"];

/** Picks a binding cloth colour that stays the same for a given book. */
function clothFor(seed: string): string {
  let hash = 0;
  for (const char of seed) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  }
  return cloths[hash % cloths.length];
}

/** The cover of a book, or a cloth binding lettered with its title. */
export function Cover({ book, className = "" }: { book: Book; className?: string }) {
  const frame =
    `relative block aspect-[2/3] w-full overflow-hidden rounded-l-[2px] rounded-r-[5px] ` +
    `shadow-[0_1px_0_rgb(27_33_30/0.25),0_10px_18px_-10px_rgb(27_33_30/0.55)] ${className}`;
  if (book.cover) {
    return (
      <span className={frame}>
        <img src={coverUrl(book)} alt="" loading="lazy" className="h-full w-full object-cover" />
        <span className="absolute inset-y-0 left-0 w-[5%] bg-gradient-to-r from-black/25 to-transparent" aria-hidden />
      </span>
    );
  }
  return (
    <span className={`${frame} @container flex flex-col text-[#f3efe4]`} style={{ backgroundColor: clothFor(book.uuid) }}>
      <span className="absolute inset-y-0 left-0 w-[9%] bg-black/20" aria-hidden />
      <span className="flex h-full flex-col justify-between py-[12%] pl-[17%] pr-[10%]" aria-hidden>
        <span className="line-clamp-5 font-display text-[10.5cqw] font-medium leading-[1.16] [overflow-wrap:anywhere]">{book.title}</span>
        <span className="line-clamp-2 border-t border-current/40 pt-[6%] text-[7cqw] leading-tight opacity-85">
          {byline(book.authors)}
        </span>
      </span>
    </span>
  );
}
