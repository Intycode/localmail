import type { SearchQuery, SearchResult } from "../types"
import { getMailService } from "./serviceRegistry"

/**
 * searchService — the only place the UI asks for search results.
 * It delegates to the MailService, keeping pages free of engine details.
 */
export async function search(query: SearchQuery): Promise<SearchResult[]> {
  const service = await getMailService()
  return service.search(query)
}
