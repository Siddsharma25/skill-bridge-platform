import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Briefcase, ChevronRight, History, Plus, Users } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchJobs } from "@/features/jobs/api";
import { getRecentlyViewedJobs } from "@/lib/storage/recentlyViewedJobs";
import { chipColor, cn, gradientButton } from "@/lib/utils";

function JobCardSkeleton() {
  return (
    <Card>
      <CardHeader>
        <div className="skeleton h-5 w-1/3 rounded-md" />
        <div className="skeleton mt-2 h-4 w-2/3 rounded-md" />
      </CardHeader>
      <CardContent className="flex gap-2">
        <div className="skeleton h-6 w-16 rounded-md" />
        <div className="skeleton h-6 w-20 rounded-md" />
      </CardContent>
    </Card>
  );
}

export function JobsPage() {
  const { data: jobs, isLoading } = useQuery({ queryKey: ["jobs"], queryFn: fetchJobs });
  // Read once per mount, not a live subscription — sessionStorage has no
  // same-tab change event, and the natural way back to this list (the
  // "Back to jobs" link) already remounts the page. See
  // lib/storage/recentlyViewedJobs.ts.
  const [recentJobs] = useState(getRecentlyViewedJobs);

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="bg-gradient-to-r from-fuchsia-600 via-violet-600 to-sky-600 bg-clip-text text-2xl font-semibold tracking-tight text-transparent">
            Job postings
          </h1>
          <p className="text-muted-foreground text-sm">Browse open roles and their skill matches.</p>
        </div>
        <Button asChild className={gradientButton}>
          <Link to="/jobs/new">
            <Plus /> New job
          </Link>
        </Button>
      </div>

      {recentJobs.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground flex items-center gap-1">
            <History className="size-3.5" /> Recently viewed (this tab):
          </span>
          {recentJobs.map((j) => (
            <Link
              key={j.id}
              to={`/jobs/${j.id}`}
              className="bg-secondary hover:bg-secondary/70 rounded-full px-2.5 py-0.5 text-xs font-medium transition-colors"
            >
              {j.title}
            </Link>
          ))}
        </div>
      )}

      {isLoading && (
        <div className="grid gap-4">
          <JobCardSkeleton />
          <JobCardSkeleton />
          <JobCardSkeleton />
        </div>
      )}

      {jobs && jobs.length === 0 && (
        <Card className="animate-in-up items-center border-dashed py-12 text-center">
          <CardContent className="flex flex-col items-center gap-3">
            <div className="flex size-14 items-center justify-center rounded-full bg-gradient-to-br from-fuchsia-400 via-violet-400 to-sky-400 shadow-lg shadow-violet-500/30">
              <Briefcase className="size-6 text-white" />
            </div>
            <div>
              <p className="font-medium">No jobs posted yet</p>
              <p className="text-muted-foreground text-sm">Post the first one to get matching started.</p>
            </div>
            <Button asChild size="sm" className={gradientButton}>
              <Link to="/jobs/new">
                <Plus /> New job
              </Link>
            </Button>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4">
        {jobs?.map((job, i) => (
          <Link key={job.id} to={`/jobs/${job.id}`} className="group block">
            <Card
              className="animate-in-up relative overflow-hidden border-l-4 hover:shadow-lg group-hover:-translate-y-0.5"
              style={{
                animationDelay: `${i * 40}ms`,
                borderLeftColor: ["#e879f9", "#a78bfa", "#38bdf8", "#34d399", "#fbbf24"][i % 5],
              }}
            >
              <CardHeader>
                <div className="flex items-center justify-between gap-2">
                  <CardTitle className="group-hover:text-primary flex items-center gap-2 transition-colors">
                    {job.title}
                    <ChevronRight className="size-4 -translate-x-1 opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100" />
                  </CardTitle>
                </div>
                <CardDescription className="line-clamp-2">{job.description}</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-wrap items-center gap-2 text-sm">
                {job.requiredSkills.map((s) => (
                  <span
                    key={s.id}
                    className={cn("rounded-full px-2.5 py-0.5 text-xs font-medium", chipColor(s.id))}
                  >
                    {s.name}
                  </span>
                ))}
                <span className="text-muted-foreground ml-auto flex items-center gap-1 text-xs">
                  <Users className="size-3.5" />
                  {job.matches.length} match{job.matches.length === 1 ? "" : "es"}
                </span>
              </CardContent>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
