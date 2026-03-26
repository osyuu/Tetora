import { ui, defaultLang, type Lang } from './ui';

export function getLangFromUrl(url: URL): Lang {
  const [, lang] = url.pathname.split('/');
  if (lang in ui) return lang as Lang;
  return defaultLang;
}

export function t(lang: Lang, key: string): string {
  return ui[lang]?.[key] ?? ui[defaultLang][key] ?? key;
}

export function getLocalizedPath(lang: Lang, path: string): string {
  return lang === defaultLang ? path : `/${lang}${path}`;
}
