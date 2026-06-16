// Let TypeScript understand `import styles from "./Foo.module.css"`.
// Vite handles the actual CSS-module transform; this just types the import as a
// className map so `styles.foo` is a string.
declare module "*.module.css" {
  const classes: { readonly [key: string]: string };
  export default classes;
}
