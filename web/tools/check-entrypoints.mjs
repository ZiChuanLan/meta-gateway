import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

// Include re-exports and literal lazy imports. Test-only files and declarations
// are not browser entry points; code shared only by tests belongs under test/.
const root = path.resolve("src");
const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) =>
  entry.isDirectory() ? walk(path.join(dir, entry.name)) : [path.join(dir, entry.name)],
);
const files = walk(root).filter((file) =>
  /\.tsx?$/.test(file) && !/(\.test\.|\.d\.ts$|[/\\]test[/\\])/.test(file),
);
const config = ts.readConfigFile("tsconfig.app.json", ts.sys.readFile);
const options = ts.parseJsonConfigFileContent(config.config, ts.sys, process.cwd()).options;
const graph = new Map();
for (const file of files) {
  const source = ts.createSourceFile(file, fs.readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
  const dependencies = [];
  const visit = (node) => {
    let specifier;
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      specifier = node.moduleSpecifier.text;
    } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword && node.arguments[0] && ts.isStringLiteral(node.arguments[0])) {
      specifier = node.arguments[0].text;
    }
    if (specifier) {
      const resolved = ts.resolveModuleName(specifier, file, options, ts.sys).resolvedModule;
      if (resolved) dependencies.push(path.resolve(resolved.resolvedFileName));
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  graph.set(file, dependencies);
}
const reachable = new Set();
const visit = (file) => {
  if (reachable.has(file)) return;
  reachable.add(file);
  for (const dependency of graph.get(file) ?? []) visit(dependency);
};
visit(path.join(root, "main.tsx"));
const orphaned = files.filter((file) => !reachable.has(file));
if (orphaned.length) {
  console.error("Files not reachable from the console entry point:");
  for (const file of orphaned) console.error(path.relative(process.cwd(), file));
  process.exitCode = 1;
} else {
  console.log(`Entry-point audit passed (${files.length} production files).`);
}
