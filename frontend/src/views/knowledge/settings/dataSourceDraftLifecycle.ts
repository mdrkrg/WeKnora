export function shouldDeleteTemporaryDataSource(input: {
  isEdit: boolean
  tempDsId: string
  isCommitted: boolean
}): boolean {
  return !input.isEdit && !input.isCommitted && input.tempDsId !== ''
}

export async function deleteTemporaryDataSourceDraft(
  input: { isEdit: boolean; tempDsId: string; isCommitted: boolean },
  deleteById: (id: string) => Promise<unknown>,
): Promise<string> {
  if (!shouldDeleteTemporaryDataSource(input)) return ''
  await deleteById(input.tempDsId)
  return input.tempDsId
}
