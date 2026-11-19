import type { ReactNode } from "react";
import { api, type Libraries } from "./api";
import { type Loaded, useLoad } from "./hooks";
import { LibrarySwitcher } from "./LibrarySwitcher";
import { NoticesProvider } from "./notices";
import { ActivityPage } from "./pages/Activity";
import { BookPage } from "./pages/Book";
import { CategoriesPage } from "./pages/Categories";
import { DevicePage } from "./pages/Device";
import { LibrariesPage } from "./pages/Libraries";
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

/**
 * Chooses the page for the current path. With no library open there is
 * one page, the libraries; until the server has said which it is, there
 * is none, so that pages do not ask a closed library for their content.
 */
function page(path: string, libraries: Loaded<Libraries>): ReactNode {
  if (!libraries.data && !libraries.error) {
    return null;
  }
  if (path === "/libraries" || (libraries.data && !libraries.data.current)) {
    return <LibrariesPage libraries={libraries} />;
  }
  const book = /^\/books\/(\d+)(\/edit)?$/.exec(path);
  if (book) {
    return <BookPage key={book[1]} id={Number(book[1])} editing={book[2] !== undefined} />;
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
 * The application frame: a bar across the top and the current page
 * under it. The bar holds the application's name, the library that is
 * open, as a control that switches to the others, and the few sections
 * of that library. That leaves the side of the window to the page: the
 * shelf uses it to browse by category, and the other pages keep to a
 * width that reads well. With no library open, the sections give way
 * to the page that opens one.
 */
export function App() {
  const path = usePath();
  const libraries = useLoad(api.libraries, []);
  const system = useLoad(api.system, []);
  const closed = libraries.data !== undefined && !libraries.data.current;
  const shelf = path === "/" && !closed;
  return (
    <SystemContext.Provider value={system.data ?? fallbackSystem}>
      <NoticesProvider>
        <div className="flex min-h-screen flex-col">
          <header className="flex flex-wrap items-center gap-x-7 gap-y-2 border-b border-rule bg-paper px-5 py-3 md:sticky md:top-0 md:z-30 md:h-16 md:flex-nowrap md:px-8 md:py-0">
            <Link to="/" className="font-display text-[1.6rem] font-medium tracking-[-0.01em]">
              Athenaeum
            </Link>
            {libraries.data && (
              <div className="min-w-0 max-w-64 flex-1 md:w-56 md:flex-none">
                <LibrarySwitcher libraries={libraries.data} onRefused={libraries.reload} />
              </div>
            )}
            <nav aria-label="Sections" className="flex basis-full gap-x-6 md:h-full md:basis-auto md:gap-x-7">
              {(closed ? [] : destinations).map(({ to, label }) => {
                const current = isCurrent(path, to);
                return (
                  <Link
                    key={to}
                    to={to}
                    aria-current={current ? "page" : undefined}
                    className={
                      "flex items-center border-b-2 py-1 md:-mb-px md:py-0 " +
                      (current ? "border-ribbon font-semibold" : "border-transparent text-soft hover:text-ink")
                    }
                  >
                    {label}
                  </Link>
                );
              })}
            </nav>
          </header>
          <main className={shelf ? "flex-1" : "mx-auto w-full max-w-[72rem] flex-1 px-5 py-7 md:px-10 md:py-9"}>
            {page(path, libraries)}
          </main>
        </div>
      </NoticesProvider>
    </SystemContext.Provider>
  );
}
