import type { CodegenConfig } from '@graphql-codegen/cli';
const config: CodegenConfig = {
 schema: '../api/internal/graph/schema.graphqls',
 documents: 'src/operations.graphql',
 generates: {
  'src/generated/graphql.ts': {
   plugins: ['typescript-operations', 'typed-document-node'],
   config: { useTypeImports: true, enumType: 'native', scalars: { Time: 'string', Upload: 'File' } },
  },
 },
};
export default config;
