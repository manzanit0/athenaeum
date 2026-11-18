import type { ReactNode } from "react";
import { api } from "./api";
import { useLoad } from "./hooks";
import { NoticesProvider } from "./notices";
import { ActivityPage } from "./pages/Activity";
import { BookPage } from "./pages/Book";
import { CategoriesPage } from "./pages/Categories";
import { DevicePage } from "./pages/Device";
import { LibraryPage } from "./pages/Library";
import { PreviewPage } from "./pages/Preview";
import { SyncPage } from "./pages/Sync";
import { Link, usePath } from "./router";
import { fallbackSystem, SystemContext } from "./system";
import { Empty } from "./ui";

const destinations = [
  { to: "/", label: "Library" },
  { to: "/kindle", label: "Kindle" },
  { to: "/sync", label: "MEGA sync" },
  { to: "/activity", label: "Activity" },
];

/** Reports whether the navigation entry to is the current section. */
function isCurrent(path: string, to: string): boolean {
  return to === "/" ? path === "/" || path === "/categories" || path.startsWith("/books/") : path.startsWith(to);
}

/** Chooses the page for the current path. */
function page(path: string): ReactNode {
  const book = /^\/books\/(\d+)$/.exec(path);
  if (book) {
    return <BookPage key={book[1]} id={Number(book[1])} />;
  }
  const preview = /^\/books\/(\d+)\/preview$/.exec(path);
  if (preview) {
    return <PreviewPage key={preview[1]} id={Number(preview[1])} />;
  }
  switch (path) {
    case "/":
      return <LibraryPage />;
    case "/categories":
      return <CategoriesPage />;
    case "/kindle":
      return <DevicePage />;
    case "/sync":
      return <SyncPage />;
    case "/activity":
      return <ActivityPage />;
  }
  return (
    <Empty title="There is nothing at this address">
      <Link to="/" className="underline underline-offset-4">
        Go to the library
      </Link>
    </Empty>
  );
}

/**
 * The application frame: navigation beside the current page. The library
 * takes the whole width of the window, which a shelf of books can fill;
 * the other pages keep to a width that reads well, centred in the space
 * beside the navigation.
 */
export function App() {
  const path = usePath();
  const system = useLoad(api.system, []);
  return (
    <SystemContext.Provider value={system.data ?? fallbackSystem}>
      <NoticesProvider>
        <div className="min-h-screen md:grid md:grid-cols-[13.5rem_minmax(0,1fr)]">
          <aside className="border-b border-rule px-5 py-4 md:sticky md:top-0 md:flex md:h-screen md:flex-col md:border-b-0 md:border-r md:px-0 md:py-8">
            <Link to="/" className="block font-display text-[1.6rem] font-medium tracking-[-0.01em] md:px-7">
              Athenaeum
            </Link>
            <nav aria-label="Sections" className="mt-3 flex flex-wrap gap-x-5 gap-y-1 md:mt-9 md:flex-col md:gap-0">
              {destinations.map(({ to, label }) => {
                const current = isCurrent(path, to);
                return (
                  <Link
                    key={to}
                    to={to}
                    aria-current={current ? "page" : undefined}
                    className={
                      "border-ribbon py-1 md:border-l-[3px] md:px-6 md:py-2 " +
                      (current
                        ? "border-b-2 font-semibold md:border-b-0"
                        : "border-transparent text-soft hover:text-ink md:border-transparent")
                    }
                  >
                    {label}
                  </Link>
                );
              })}
            </nav>
            {system.data && (
              <p className="mt-auto hidden px-7 text-[0.87rem] text-soft md:block">Version {system.data.version}</p>
            )}
          </aside>
          <main className={`w-full px-5 py-7 md:px-10 md:py-9 ${path === "/" ? "" : "mx-auto max-w-[72rem]"}`}>{page(path)}</main>
        </div>
      </NoticesProvider>
    </SystemContext.Provider>
  );
}
