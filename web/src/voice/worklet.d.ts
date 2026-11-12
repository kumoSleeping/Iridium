// A worklet's file, by its address: a file of its own, never inlined (the page's CSP takes
// scripts from here only, no data: nor blob:).
declare module "*.worklet.js?url&no-inline" {
  const url: string;
  export default url;
}
