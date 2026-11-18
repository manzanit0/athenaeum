import { type ReactNode, useState } from "react";
import { api, Refusal } from "./api";
import { messageOf, sentence } from "./format";
import { useNotices } from "./notices";
import { Confirm } from "./ui";

/** A request to open the library in a folder; create allows starting one there. */
export interface Opening {
  path: string;
  create: boolean;
}

/**
 * Returns the way to open a library and the dialog that goes with it.
 * Opening one closes the other, so when something is still running in
 * the open library the dialog asks before stopping it. Once the library
 * is open the interface is loaded afresh on the shelf, since everything
 * on screen belonged to the previous library. A refusal is shown as a
 * notice and reported to onRefused.
 */
export function useOpener(onRefused: () => void): { open: (opening: Opening) => void; dialog: ReactNode } {
  const { notify } = useNotices();
  const [interrupting, setInterrupting] = useState<Opening>();
  const open = async (opening: Opening, force = false) => {
    try {
      await api.openLibrary(opening.path, opening.create, force);
      window.location.assign("/");
    } catch (reason) {
      if (reason instanceof Refusal && reason.code === "busy") {
        setInterrupting(opening);
        return;
      }
      notify(sentence(messageOf(reason)), "error");
      onRefused();
    }
  };
  const dialog = (
    <Confirm
      open={interrupting !== undefined}
      title="Something is still running"
      action="Stop it and open"
      onCancel={() => setInterrupting(undefined)}
      onConfirm={() => {
        const opening = interrupting;
        setInterrupting(undefined);
        if (opening) {
          open(opening, true);
        }
      }}
    >
      A sync, conversion or transfer is running in the open library. Opening another library stops it.
    </Confirm>
  );
  return { open: (opening) => open(opening), dialog };
}
