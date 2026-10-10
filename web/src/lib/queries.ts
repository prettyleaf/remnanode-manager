import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { api } from "./api"
import type { AboutResponse, AppEvent, AuthState, SettingsResponse, StatusResponse } from "./types"

export function useAuthState() {
  return useQuery({
    queryKey: ["auth"],
    queryFn: () => api<AuthState>("/auth/state"),
    staleTime: 60_000,
  })
}

export function useStatus() {
  return useQuery({
    queryKey: ["status"],
    queryFn: () => api<StatusResponse>("/status"),
    refetchInterval: 5_000,
  })
}

export function useAbout() {
  return useQuery({
    queryKey: ["about"],
    queryFn: () => api<AboutResponse>("/about"),
    staleTime: 60_000,
    refetchInterval: 10 * 60_000,
  })
}

export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsResponse>("/settings"),
    staleTime: Infinity,
  })
}

export interface EventFilters {
  type: string
  level: string
  search: string
}

export function useEvents(filters: EventFilters) {
  return useInfiniteQuery({
    queryKey: ["events", filters],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const q = new URLSearchParams({ limit: "50" })
      if (filters.type) q.set("type", filters.type)
      if (filters.level) q.set("level", filters.level)
      if (filters.search) q.set("search", filters.search)
      if (pageParam) q.set("before", String(pageParam))
      return api<{ events: AppEvent[] }>(`/events?${q}`)
    },
    getNextPageParam: (last) =>
      last.events.length === 50 ? last.events[last.events.length - 1].id : undefined,
    refetchInterval: 15_000,
  })
}
