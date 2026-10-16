/**
 * The top-bar search on pages that hold a list of their own (quotes,
 * bookmarks, tags, collections): it filters that list as you type
 * rather than sending you to the file search. The query lives in the page's
 * URL (`?q=`), so a filtered view survives a reload and the back button.
 */

import { useState } from 'react';
import type { KeyboardEvent, RefObject } from 'react';
import { useSearchParams } from 'react-router-dom';

import { Icon } from '../ui/Icon';
import { PAGE_QUERY_PARAM } from './pageSearch';
import './SearchBox.css';

export function PageFilterSearch({
  noun,
  inputRef,
}: {
  noun: string;
  inputRef?: RefObject<HTMLInputElement | null>;
}) {
  const [params, setParams] = useSearchParams();
  const urlQuery = params.get(PAGE_QUERY_PARAM) ?? '';
  // Local state keeps typing smooth; the URL follows it (replace, so typing
  // doesn't fill history). An outside change to the URL wins.
  const [query, setQuery] = useState(urlQuery);
  const [prevUrlQuery, setPrevUrlQuery] = useState(urlQuery);
  if (prevUrlQuery !== urlQuery) {
    setPrevUrlQuery(urlQuery);
    if (urlQuery !== query) setQuery(urlQuery);
  }

  const apply = (value: string) => {
    setQuery(value);
    setPrevUrlQuery(value);
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(PAGE_QUERY_PARAM, value);
        else next.delete(PAGE_QUERY_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      if (query) apply('');
      else inputRef?.current?.blur();
    }
  };

  const label = `Search ${noun}`;

  return (
    <form className="search-box" role="search" onSubmit={(e) => e.preventDefault()}>
      <Icon name="search" className="search-box-icon" />
      <input
        ref={inputRef}
        type="search"
        aria-label={label}
        placeholder={`${label}…`}
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(event) => apply(event.target.value)}
        onKeyDown={onKeyDown}
        data-testid="page-filter-search"
      />
      {query && (
        <button
          type="button"
          className="search-box-clear"
          aria-label="Clear search"
          onClick={() => {
            apply('');
            inputRef?.current?.focus();
          }}
        >
          <Icon name="close" size={18} />
        </button>
      )}
      <kbd className="search-box-kbd" aria-hidden="true">
        /
      </kbd>
    </form>
  );
}
