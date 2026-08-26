import { describe, it, expect, beforeEach } from "vitest";
import { recordJobView, getRecentlyViewedJobs } from "@/lib/storage/recentlyViewedJobs";

describe("recentlyViewedJobs (sessionStorage-backed)", () => {
  // jsdom provides a real sessionStorage — cleared between tests so one
  // test's recorded views can't leak into the next.
  beforeEach(() => {
    sessionStorage.clear();
  });

  it("returns an empty list when nothing has been viewed yet", () => {
    expect(getRecentlyViewedJobs()).toEqual([]);
  });

  it("records a view and returns it newest-first", () => {
    recordJobView({ id: "job-1", title: "Backend Engineer" });
    recordJobView({ id: "job-2", title: "Frontend Engineer" });

    expect(getRecentlyViewedJobs()).toEqual([
      { id: "job-2", title: "Frontend Engineer" },
      { id: "job-1", title: "Backend Engineer" },
    ]);
  });

  it("de-duplicates by id, moving a re-viewed job back to the front", () => {
    recordJobView({ id: "job-1", title: "Backend Engineer" });
    recordJobView({ id: "job-2", title: "Frontend Engineer" });
    recordJobView({ id: "job-1", title: "Backend Engineer" }); // viewed again

    const jobs = getRecentlyViewedJobs();
    expect(jobs).toHaveLength(2);
    expect(jobs[0].id).toBe("job-1");
  });

  it("caps the list at 5 entries, dropping the oldest", () => {
    for (let i = 1; i <= 6; i++) {
      recordJobView({ id: `job-${i}`, title: `Job ${i}` });
    }

    const jobs = getRecentlyViewedJobs();
    expect(jobs).toHaveLength(5);
    // job-1 was the first viewed and the 6th distinct view pushed it out.
    expect(jobs.some((j) => j.id === "job-1")).toBe(false);
    expect(jobs[0].id).toBe("job-6");
  });
});
