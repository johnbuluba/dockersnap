// API-token bootstrap. Mirrors the CLI's DOCKERSNAP_TOKEN model: the user
// pastes a token into the top bar; we persist it in localStorage and ship
// it as an Authorization: Bearer header on every request. Tokenless setups
// (the default) leave this null and everything still works.
import { useEffect, useState } from "preact/hooks";

const STORAGE_KEY = "dockersnap.token";

function read(): string | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return v && v.length > 0 ? v : null;
  } catch {
    return null;
  }
}

export function useToken(): [string | null, (next: string | null) => void] {
  const [token, setTokenState] = useState<string | null>(read);

  useEffect(() => {
    try {
      if (token) localStorage.setItem(STORAGE_KEY, token);
      else localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore — falls back to in-memory only
    }
  }, [token]);

  return [token, setTokenState];
}

/** getToken is the non-React reader for use inside fetch wrappers. */
export function getToken(): string | null {
  return read();
}
